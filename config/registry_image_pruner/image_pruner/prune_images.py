import argparse
import itertools
import json
import logging
import os
import re
import time

from collections.abc import Iterator, Sequence, Generator
from contextlib import suppress
from datetime import UTC, datetime, timedelta
from http.client import HTTPResponse
from typing import Any, Dict, List, Final
from urllib.error import HTTPError
from urllib.parse import urlencode
from urllib.request import Request, urlopen

logging.basicConfig(format="%(asctime)s - %(levelname)s - %(message)s", level=logging.INFO)
LOGGER = logging.getLogger(__name__)
QUAY_API_URL = "https://quay.io/api/v1"
RETRY_LIMIT = 5

ARTIFACTS_TAGS_SUFFIX: Final = [".sbom", ".att", ".src", ".sig", ".dockerfile"]
SUFFIX_PATTERN: Final = "|".join(ARTIFACTS_TAGS_SUFFIX).replace(".", "\\.")
ARTIFACT_TAG_REGEX: Final = rf"(?P<digest>sha256-[0-9a-f]{{64}})({SUFFIX_PATTERN})"

processed_repos_counter = itertools.count()
deleted_tags_counter = itertools.count()


ImageRepo = Dict[str, Any]


class TimeRange:
    __slots__ = ("from_ts", "to_ts")

    def __init__(self, from_ts: float, to_ts: float) -> None:
        """Initialize an instance

        :param from_ts: from this UTC timestamp (newer).
        :type from_ts: int
        :param to_ts: to this UTC timestamp (older).
        :type to_ts: int
        """
        self.from_ts = from_ts
        self.to_ts = to_ts

    @classmethod
    def past_days(cls, days: int, since: datetime | None = None) -> "TimeRange":
        if since is None:
            since = datetime.now(UTC)
        to = since - timedelta(days=days)
        return TimeRange(from_ts=since.timestamp(), to_ts=to.timestamp())

    def __str__(self) -> str:
        from_dt = datetime.fromtimestamp(self.from_ts, UTC)
        to_dt = datetime.fromtimestamp(self.to_ts, UTC)
        return f"Time range from {from_dt} ({self.from_ts}) to {to_dt} ({self.to_ts})"

    def __repr__(self) -> str:
        return self.__str__()


class SubjectImageDigestCollector:
    """Collect digests of subject images that have artifacts"""

    def __init__(self, time_range: TimeRange | None = None) -> None:
        self._tr = time_range
        self._image_digests = set([])
        self._artifact_tag_regex = re.compile(ARTIFACT_TAG_REGEX)
        self._is_out_of_time_range = False
        self._handled_tags_count = 0

    @property
    def image_digests(self) -> list[str]:
        return sorted(self._image_digests)

    @property
    def handled_tags_count(self) -> int:
        return self._handled_tags_count

    @staticmethod
    def _get_required_attributes(pairs: list[tuple[str, Any]]) -> tuple[Any, Any, Any]:
        name = None
        manifest_digest = None
        start_ts = None
        for key, val in pairs:
            if key == "name":
                name = val
            elif key == "manifest_digest":
                manifest_digest = val
            elif key == "start_ts":
                start_ts = val
        return name, manifest_digest, start_ts

    def __call__(self, pairs: list[tuple[str, Any]]) -> dict[str, Any] | None:
        name, manifest_digest, start_ts = self._get_required_attributes(pairs)

        if not (name and manifest_digest and start_ts):
            obj = dict(pairs)
            if "tags" in obj and self._is_out_of_time_range:
                obj["out_of_time_range"] = self._is_out_of_time_range
            return obj

        self._handled_tags_count += 1

        if self._tr and (start_ts < self._tr.to_ts or start_ts > self._tr.from_ts):
            self._is_out_of_time_range = True
            return None

        if match := self._artifact_tag_regex.fullmatch(name):
            image_digest = match.group("digest").replace("-", ":")
            self._image_digests.add(image_digest)
            return None

        # Assume this is the subject image, remove this digest from candidate list
        with suppress(KeyError):
            self._image_digests.remove(manifest_digest)

        return None


def get_quay_tags(quay_token: str, namespace: str, name: str) -> Generator[bytes]:
    """Fetch tags information from Quay.io and yield them by page

    :param quay_token: Quay.io OAuth2 token used to access repositories.
    :type quay_token: str
    :param namespace: Quay organization. For a given repository ``quay.io/redhat-user-workloads/someone-tenant/image``,
        ``redhat-user-workloads`` is the one.
    :type namespace: str
    :param name: The image repository name without namespace. Take the example of argument ``namespace``,
        ``someone-tenant/image`` is the name.
    :type quay_token: str
    """
    resp: HTTPResponse

    retry_count = 0
    query_args = {"limit": 100, "onlyActiveTags": True, "page": 0}
    request = Request(QUAY_API_URL, headers={"Authorization": f"Bearer {quay_token}"})
    total_content_len = 0

    while True:
        query_args["page"] += 1
        api_url = f"{QUAY_API_URL}/repository/{namespace}/{name}/tag/?{urlencode(query_args)}"
        request.full_url = api_url
        LOGGER.debug("Request tags: %s", api_url)

        try:
            with urlopen(request) as resp:
                if resp.status != 200:
                    raise RuntimeError(resp.reason)
                content = resp.read()
                content_len = len(content)
                total_content_len += content_len
                LOGGER.debug("Length of got response: %s. In total so far: %s", content_len, total_content_len)
                yield content
                retry_count = 0
        except HTTPError as ex:
            if ex.status == 404:
                LOGGER.info("Repository doesn't exist anymore %s/%s", namespace, name)
                return

            if ex.status == 502 or ex.status == 504:
                if retry_count > RETRY_LIMIT:
                    LOGGER.info("Gateway error, retry reached retry limit %s", RETRY_LIMIT)
                    raise

                retry_count += 1
                LOGGER.info("Gateway error, will retry")
                time.sleep(2)
                continue
            raise


def collect_subject_image_digests(list_quay_tags: Generator[bytes], collector: SubjectImageDigestCollector) -> None:
    for content in list_quay_tags:
        json_data = json.loads(content, object_pairs_hook=collector)
        tags = json_data.get("tags", [])
        if not tags:
            LOGGER.debug("No tags found.")
            break
        if json_data.get("out_of_time_range"):
            LOGGER.info("Reached tag which is out of time range. Stop handling additional tags.")
            break
        if not json_data.get("has_additional", False):
            LOGGER.info("No additional tags found.")
            break


def delete_image_tag(quay_token: str, namespace: str, name: str, tag: str) -> None:
    api_url = f"{QUAY_API_URL}/repository/{namespace}/{name}/tag/{tag}"
    request = Request(
        api_url,
        method="DELETE",
        headers={
            "Authorization": f"Bearer {quay_token}",
        },
    )
    resp: HTTPResponse
    try:
        with urlopen(request) as resp:
            if resp.status != 200 and resp.status != 204:
                raise RuntimeError(resp.reason)

    except HTTPError as ex:
        # ignore if not found
        if ex.status != 404:
            raise (ex)


# FIXME: use HEAD
def manifest_exists(quay_token: str, namespace: str, name: str, manifest: str) -> bool:
    api_url = f"{QUAY_API_URL}/repository/{namespace}/{name}/manifest/{manifest}"
    request = Request(
        api_url,
        headers={
            "Authorization": f"Bearer {quay_token}",
        },
    )
    resp: HTTPResponse
    manifest_exists = True
    try:
        with urlopen(request) as resp:
            if resp.status != 200 and resp.status != 204:
                raise RuntimeError(resp.reason)

    except HTTPError as ex:
        if ex.status != 404:
            raise (ex)
        else:
            manifest_exists = False

    return manifest_exists


def remove_tags(
    image_digests: Sequence[str], quay_token: str, namespace: str, name: str, dry_run: bool = False
) -> None:
    for image_digest in image_digests:
        if manifest_exists(quay_token, namespace, name, image_digest):
            LOGGER.info("Image manifest still exists: %s", image_digest)
            continue

        digest_in_tag = image_digest.replace(":", "-")
        tags_to_remove = [f"{digest_in_tag}{suffix}" for suffix in ARTIFACTS_TAGS_SUFFIX]
        for tag_name in tags_to_remove:
            if dry_run:
                LOGGER.info("Tag %s from %s/%s should be removed", tag_name, namespace, name)
            else:
                tags_count = next(deleted_tags_counter)
                LOGGER.info("Removing tag %s: %s from %s/%s", tags_count, tag_name, namespace, name)
                delete_image_tag(quay_token, namespace, name, tag_name)


def process_repositories(
    repos: List[ImageRepo], quay_token: str, dry_run: bool = False, time_range: TimeRange | None = None
) -> None:
    for repo in repos:
        namespace = repo["namespace"]
        name = repo["name"]
        LOGGER.info("Processing repository %s: %s/%s", next(processed_repos_counter), namespace, name)

        collector = SubjectImageDigestCollector(time_range)
        list_quay_tags = get_quay_tags(quay_token, namespace, name)
        collect_subject_image_digests(list_quay_tags, collector)
        LOGGER.info("Handled tags in total: %s", collector.handled_tags_count)
        image_digests = collector.image_digests
        LOGGER.debug("Collected subject image digests: %s", len(image_digests))
        remove_tags(image_digests, quay_token, namespace, name, dry_run=dry_run)


def fetch_image_repos(access_token: str, namespace: str) -> Iterator[List[ImageRepo]]:
    next_page = None
    resp: HTTPResponse
    while True:
        query_args = {"namespace": namespace}
        if next_page is not None:
            query_args["next_page"] = next_page

        api_url = f"{QUAY_API_URL}/repository?{urlencode(query_args)}"
        request = Request(
            api_url,
            headers={
                "Authorization": f"Bearer {access_token}",
            },
        )

        with urlopen(request) as resp:
            if resp.status != 200:
                raise RuntimeError(resp.reason)
            json_data = json.loads(resp.read())

        repos = json_data.get("repositories", [])
        if not repos:
            LOGGER.debug("No image repository is found.")
            break

        yield repos

        if (next_page := json_data.get("next_page", None)) is None:
            break


def main():
    args = parse_args()

    if args.verbose:
        LOGGER.setLevel(logging.DEBUG)

    token = os.getenv("QUAY_TOKEN")
    if not token:
        raise ValueError("The token required for access to Quay API is missing!")

    time_range = None
    if args.past_days:
        time_range = TimeRange.past_days(args.past_days)

    for image_repos in fetch_image_repos(token, args.namespace):
        process_repositories(image_repos, token, dry_run=args.dry_run, time_range=time_range)

    LOGGER.info(
        "Total: %s processed repositories, %s deleted tags.",
        next(processed_repos_counter) - 1,
        next(deleted_tags_counter) - 1,
    )


def parse_args():
    parser = argparse.ArgumentParser()
    parser.add_argument("--namespace", required=True, help="Quay organization name, e.g. redhat-user-workloads.")
    parser.add_argument("--dry-run", action="store_true", help="Dry run without actually deleting tags.")
    parser.add_argument("-v", "--verbose", action="store_true", help="Run in verbose mode.")
    parser.add_argument(
        "-d",
        "--in-past-days",
        dest="past_days",
        metavar="DAYS",
        type=int,
        help="Handle tags generated in the past N days since script starts to run.",
    )
    args = parser.parse_args()
    return args


if __name__ == "__main__":
    main()
