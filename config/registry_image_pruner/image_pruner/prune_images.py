import argparse
import itertools
import json
import logging
import os
import re
import time
from collections.abc import Iterator
from datetime import UTC, datetime, timedelta
from http.client import HTTPResponse
from typing import Any, Dict, List
from urllib.error import HTTPError
from urllib.parse import urlencode
from urllib.request import Request, urlopen

logging.basicConfig(format="%(asctime)s - %(levelname)s - %(message)s", level=logging.INFO)
LOGGER = logging.getLogger(__name__)
QUAY_API_URL = "https://quay.io/api/v1"
RETRY_LIMIT = 5

processed_repos_counter = itertools.count()


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


def get_quay_tags(quay_token: str, namespace: str, name: str, time_range: TimeRange | None = None) -> List[ImageRepo]:
    """Fetch tags information from Quay.io

    :param quay_token: Quay.io OAuth2 token used to access repositories.
    :type quay_token: str
    :param namespace: Quay organization. For a given repository ``quay.io/redhat-user-workloads/someone-tenant/image``,
        ``redhat-user-workloads`` is the one.
    :type namespace: str
    :param name: The image repository name without namespace. Take the example of argument ``namespace``,
        ``someone-tenant/image`` is the name.
    :type quay_token: str
    :param time_range: return tags whose creation time is in this time range.
        If omitted, time is not considered as a filter condition.
    :type time_range: :class:`TimeRange`
    """
    next_page = None
    resp: HTTPResponse

    all_tags = []
    retry_count = 0
    if time_range:
        from_ts, to_ts = time_range.from_ts, time_range.to_ts
    else:
        from_ts, to_ts = None, None
    stop_query = False
    while True:
        query_args = {"limit": 100, "onlyActiveTags": True}
        if next_page is not None:
            query_args["page"] = next_page

        api_url = f"{QUAY_API_URL}/repository/{namespace}/{name}/tag/?{urlencode(query_args)}"
        request = Request(
            api_url,
            headers={
                "Authorization": f"Bearer {quay_token}",
            },
        )

        try:
            with urlopen(request) as resp:
                if resp.status != 200:
                    raise RuntimeError(resp.reason)
                json_data = json.loads(resp.read())
                retry_count = 0
        except HTTPError as ex:
            if ex.status == 404:
                LOGGER.info("Repository doesn't exist anymore %s/%s", namespace, name)
                return []

            if ex.status == 502 or ex.status == 504:
                if retry_count > RETRY_LIMIT:
                    LOGGER.info("Gateway error, retry reached retry limit %s", RETRY_LIMIT)
                    raise

                retry_count += 1
                LOGGER.info("Gateway error, will retry")
                time.sleep(2)
                continue
            raise

        tags = json_data.get("tags", [])

        for tag in tags:
            # store only name & manifest_digest keys, as others aren't used and take memory
            tag_info = {"name": tag["name"], "manifest_digest": tag["manifest_digest"]}
            if time_range:
                if to_ts <= tag["start_ts"] <= from_ts:
                    all_tags.append(tag_info)
                else:
                    stop_query = True
                    break
            else:
                all_tags.append(tag_info)

        if stop_query:
            break

        if not tags:
            LOGGER.debug("No tags found.")
            break

        page = json_data.get("page", None)
        additional = json_data.get("has_additional", False)

        if additional:
            next_page = page + 1
        else:
            break

    return all_tags


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


def remove_tags(tags: List[Dict[str, Any]], quay_token: str, namespace: str, name: str, dry_run: bool = False) -> None:
    tags_map = {tag_info["name"]: tag_info["manifest_digest"] for tag_info in tags}
    # delete tags to save memory
    del tags
    image_digests = [digest for _, digest in tags_map.items()]
    tag_regex = re.compile(r"^sha256-([0-9a-f]+)(\.sbom|\.att|\.src|\.sig|\.dockerfile)$")
    manifests_checked = {}
    for tag_name in tags_map:
        # attestation or sbom image
        if (match := tag_regex.match(tag_name)) is not None:
            if f"sha256:{match.group(1)}" not in image_digests:
                # verify that manifest really doesn't exist, because if tag was
                # removed, it won't be in tag list, but may still be in the
                # registry
                manifest_existence = manifests_checked.get(f"sha256:{match.group(1)}")
                if manifest_existence is None:
                    manifest_existence = manifest_exists(quay_token, namespace, name, f"sha256:{match.group(1)}")
                    manifests_checked[f"sha256:{match.group(1)}"] = manifest_existence

                if not manifest_existence:
                    if dry_run:
                        LOGGER.info("Tag %s from %s/%s should be removed", tag_name, namespace, name)
                    else:
                        LOGGER.info("Removing tag %s from %s/%s", tag_name, namespace, name)
                        delete_image_tag(quay_token, namespace, name, tag_name)

        elif tag_name.endswith(".src"):
            to_delete = False

            binary_tag = tag_name.removesuffix(".src")
            if binary_tag not in tags_map:
                to_delete = True
            else:
                manifest_digest = tags_map[binary_tag]
                new_src_tag = f"{manifest_digest.replace(':', '-')}.src"
                to_delete = new_src_tag in tags_map

            if to_delete:
                LOGGER.info("Removing deprecated tag %s", tag_name)
                if not dry_run:
                    delete_image_tag(quay_token, namespace, name, tag_name)
        else:
            LOGGER.debug("%s is not in a known type to be deleted.", tag_name)


def process_repositories(
    repos: List[ImageRepo], quay_token: str, dry_run: bool = False, time_range: TimeRange | None = None
) -> None:
    for repo in repos:
        namespace = repo["namespace"]
        name = repo["name"]
        LOGGER.info("Processing repository %s: %s/%s", next(processed_repos_counter), namespace, name)

        all_tags = get_quay_tags(quay_token, namespace, name, time_range=time_range)

        if not all_tags:
            continue

        remove_tags(all_tags, quay_token, namespace, name, dry_run=dry_run)


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
