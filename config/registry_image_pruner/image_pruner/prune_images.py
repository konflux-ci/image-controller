import argparse
import itertools
import json
import logging
import os
import re

from collections.abc import Iterator
from http.client import HTTPResponse
from typing import Any, Dict, List
from urllib.parse import urlencode
from urllib.request import Request, urlopen

logging.basicConfig(
    format="%(asctime)s - %(levelname)s - %(message)s", level=logging.INFO
)
LOGGER = logging.getLogger(__name__)
QUAY_API_URL = "https://quay.io/api/v1"

processed_repos_counter = itertools.count()


ImageRepo = Dict[str, Any]


def get_quay_repo(quay_token: str, namespace: str, name: str) -> ImageRepo:
    api_url = f"{QUAY_API_URL}/repository/{namespace}/{name}"
    request = Request(api_url, headers={
        "Authorization": f"Bearer {quay_token}",
    })
    resp: HTTPResponse
    with urlopen(request) as resp:
        if resp.status != 200:
            raise RuntimeError(resp.reason)
        return json.loads(resp.read())


def delete_image_tag(quay_token: str, namespace: str, name: str, tag: str) -> None:
    api_url = f"{QUAY_API_URL}/repository/{namespace}/{name}/tag/{tag}"
    request = Request(api_url, method="DELETE", headers={
        "Authorization": f"Bearer {quay_token}",
    })
    resp: HTTPResponse
    with urlopen(request) as resp:
        if resp.status != 200 and resp.status != 204:
            raise RuntimeError(resp.reason)


def remove_tags(tags: Dict[str, Any], quay_token: str, namespace: str, name: str, dry_run: bool = False) -> None:
    image_digests = [image["manifest_digest"] for image in tags.values()]
    tag_regex = re.compile(r"^sha256-([0-9a-f]+)(\.sbom|\.att|\.src)$")
    for tag in tags:
        # attestation or sbom image
        if (match := tag_regex.match(tag)) is not None:
            if f"sha256:{match.group(1)}" not in image_digests:
                if dry_run:
                    LOGGER.info("Image %s from %s/%s should be removed", tag, namespace, name)
                else:
                    LOGGER.info("Removing image %s from %s/%s", tag, namespace, name)
                    delete_image_tag(quay_token, namespace, name, tag)
        else:
            LOGGER.debug("%s is not an image with suffix .att or .sbom", tag)


def process_repositories(repos: List[ImageRepo], quay_token: str, dry_run: bool = False) -> None:
    for repo in repos:
        namespace = repo["namespace"]
        name = repo["name"]
        LOGGER.info("Processing repository %s: %s/%s", next(processed_repos_counter), namespace, name)
        repo_info = get_quay_repo(quay_token, namespace, name)
        if (tags := repo_info.get("tags")) is not None:
            remove_tags(tags, quay_token, namespace, name, dry_run=dry_run)


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
