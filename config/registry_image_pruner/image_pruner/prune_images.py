import argparse
import itertools
import json
import logging
import os
import re
import time

from collections.abc import Iterator
from http.client import HTTPResponse
from typing import Any, Dict, List
from urllib.error import HTTPError
from urllib.parse import urlencode
from urllib.request import Request, urlopen

logging.basicConfig(
    format="%(asctime)s - %(levelname)s - %(message)s", level=logging.INFO
)
LOGGER = logging.getLogger(__name__)
QUAY_API_URL = "https://quay.io/api/v1"
RETRY_LIMIT = 5

processed_repos_counter = itertools.count()


ImageRepo = Dict[str, Any]


def get_quay_tags(quay_token: str, namespace: str, name: str) -> List[ImageRepo]:
    next_page = None
    resp: HTTPResponse

    all_tags = []
    retry_count = 0
    while True:
        query_args = {"limit": 100, "onlyActiveTags": True}
        if next_page is not None:
            query_args["page"] = next_page

        api_url = f"{QUAY_API_URL}/repository/{namespace}/{name}/tag/?{urlencode(query_args)}"
        request = Request(api_url, headers={
            "Authorization": f"Bearer {quay_token}",
        })

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

            if ex.status == 502:
                if retry_count > RETRY_LIMIT:
                    LOGGER.info("Bad gateway, retry reached retry limit %s", RETRY_LIMIT)
                    raise

                retry_count += 1
                LOGGER.info("Bad gateway, will retry")
                time.sleep(2)
                continue
            raise

        tags = json_data.get("tags", [])
        all_tags.extend(tags)

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
    request = Request(api_url, method="DELETE", headers={
        "Authorization": f"Bearer {quay_token}",
    })
    resp: HTTPResponse
    try:
        with urlopen(request) as resp:
            if resp.status != 200 and resp.status != 204:
                raise RuntimeError(resp.reason)

    except HTTPError as ex:
        # ignore if not found
        if ex.status != 404:
            raise(ex)


def manifest_exists(quay_token: str, namespace: str, name: str, manifest: str) -> bool:
    api_url = f"{QUAY_API_URL}/repository/{namespace}/{name}/manifest/{manifest}"
    request = Request(api_url, headers={
        "Authorization": f"Bearer {quay_token}",
    })
    resp: HTTPResponse
    manifest_exists = True
    try:
        with urlopen(request) as resp:
            if resp.status != 200 and resp.status != 204:
                raise RuntimeError(resp.reason)

    except HTTPError as ex:
        if ex.status != 404:
            raise(ex)
        else:
            manifest_exists = False

    return manifest_exists


def remove_tags(tags: List[Dict[str, Any]], quay_token: str, namespace: str, name: str, dry_run: bool = False) -> None:
    image_digests = [image["manifest_digest"] for image in tags]
    tags_map = {tag_info["name"]: tag_info for tag_info in tags}
    tag_regex = re.compile(r"^sha256-([0-9a-f]+)(\.sbom|\.att|\.src|\.sig|\.dockerfile)$")
    manifests_checked = {}
    for tag in tags:
        # attestation or sbom image
        if (match := tag_regex.match(tag["name"])) is not None:
            if f"sha256:{match.group(1)}" not in image_digests:
                # verify that manifest really doesn't exist, because if tag was removed, it won't be in tag list, but may still be in the registry
                manifest_existence = manifests_checked.get(f"sha256:{match.group(1)}")
                if manifest_existence is None:
                    manifest_existence = manifest_exists(quay_token, namespace, name, f"sha256:{match.group(1)}")
                    manifests_checked[f"sha256:{match.group(1)}"] = manifest_existence

                if not manifest_existence:
                    if dry_run:
                        LOGGER.info("Tag %s from %s/%s should be removed", tag["name"], namespace, name)
                    else:
                        LOGGER.info("Removing tag %s from %s/%s", tag["name"], namespace, name)
                        delete_image_tag(quay_token, namespace, name, tag["name"])

        elif tag["name"].endswith(".src"):
            to_delete = False

            binary_tag = tag["name"].removesuffix(".src")
            if binary_tag not in tags_map:
                to_delete = True
            else:
                manifest_digest = tags_map[binary_tag]["manifest_digest"]
                new_src_tag = f"{manifest_digest.replace(':', '-')}.src"
                to_delete = new_src_tag in tags_map

            if to_delete:
                LOGGER.info("Removing deprecated tag %s", tag["name"])
                delete_image_tag(quay_token, namespace, name, tag["name"])
        else:
            LOGGER.debug("%s is not in a known type to be deleted.", tag["name"])


def process_repositories(repos: List[ImageRepo], quay_token: str, dry_run: bool = False) -> None:
    for repo in repos:
        namespace = repo["namespace"]
        name = repo["name"]
        LOGGER.info("Processing repository %s: %s/%s", next(processed_repos_counter), namespace, name)
        all_tags = get_quay_tags(quay_token, namespace, name)

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
        request = Request(api_url, headers={
            "Authorization": f"Bearer {access_token}",
        })

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
    token = os.getenv("QUAY_TOKEN")
    if not token:
        raise ValueError("The token required for access to Quay API is missing!")

    args = parse_args()

    for image_repos in fetch_image_repos(token, args.namespace):
        process_repositories(image_repos, token, dry_run=args.dry_run)


def parse_args():
    parser = argparse.ArgumentParser()
    parser.add_argument("--namespace", required=True)
    parser.add_argument("--dry-run", action="store_true")
    args = parser.parse_args()
    return args


if __name__ == "__main__":
    main()
