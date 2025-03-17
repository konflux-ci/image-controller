import argparse
import itertools
import json
import logging
import os

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

processed_repos_counter = itertools.count()

ImageRepo = Dict[str, Any]
RepoNotification = Dict[str, Any]


def get_quay_notifications(
    quay_token: str, namespace: str, name: str
) -> List[RepoNotification]:
    """
    Get all notifications for a repository
    Quay API response format:
    {
        "notifications": [
            {
                "uuid": "string",
                "number_of_failures": int,
                "title": "string",
                ...
            },
            ...
        ]
    }
    """
    resp: HTTPResponse

    api_url = f"{QUAY_API_URL}/repository/{namespace}/{name}/notification/"
    request = Request(
        api_url,
        headers={
            "Authorization": f"Bearer {quay_token}",
        },
    )

    with urlopen(request) as resp:
        if resp.status != 200:
            # do not fail the job if we can't fetch notifications
            # for single repository
            LOGGER.warning("Failed to fetch notifications for %s/%s", namespace, name)
            json_data = {}
        else:
            json_data = json.loads(resp.read())

    return json_data.get("notifications", [])


def reset_notification(uuid: str, quay_token: str, namespace: str, name: str) -> None:
    """Reset notification by notification uuid"""
    api_url = f"{QUAY_API_URL}/repository/{namespace}/{name}/notification/{uuid}"
    request = Request(
        api_url,
        method="POST",
        headers={
            "Authorization": f"Bearer {quay_token}",
        },
    )
    resp: HTTPResponse
    try:
        with urlopen(request) as resp:
            # The actual API response is 204 for notification reset
            # There is bug in Quay Swagger docs generator
            # claiming all POST request return 201
            if resp.status not in (201, 204):
                # do not fail the job if we can't reset notification
                LOGGER.warning(
                    "Failed to reset notification %s from %s/%s",
                    uuid,
                    namespace,
                    name,
                )
    except HTTPError as ex:
        # Quay API returns 400 if notification is not found
        # filter out when this is the case
        rsp_message = json.loads(ex.read()).get("detail", "")
        if ex.status == 400 and rsp_message.startswith(
            "No repository notification found"
        ):
            LOGGER.info(
                "Notification %s from %s/%s was not found", uuid, namespace, name
            )
        else:
            LOGGER.warning(
                "Failed to reset notification %s from %s/%s with error: %s",
                uuid,
                namespace,
                name,
                rsp_message,
            )


def process_repositories(
    repos: List[ImageRepo], quay_token: str, dry_run: bool = False
) -> None:
    """Process all repositories and reset notifications if needed"""
    for repo in repos:
        namespace = repo["namespace"]
        name = repo["name"]
        LOGGER.info(
            "Processing repository %s: %s/%s",
            next(processed_repos_counter),
            namespace,
            name,
        )
        all_notifications = get_quay_notifications(quay_token, namespace, name)

        if not all_notifications:
            continue

        for notification in all_notifications:
            notification_title = notification.get("title", "")
            uuid = notification["uuid"]
            if notification.get("number_of_failures", 0) > 0:
                if dry_run:
                    LOGGER.info(
                        "Notification %s with title %s from %s/%s should be reset",
                        uuid,
                        notification_title,
                        namespace,
                        name,
                    )
                else:
                    reset_notification(uuid, quay_token, namespace, name)
                    LOGGER.info(
                        "Notification %s with title %s from %s/%s was reset",
                        uuid,
                        notification_title,
                        namespace,
                        name,
                    )
            else:
                LOGGER.info(
                    "Notification %s with title %s from %s/%s has no failures",
                    uuid,
                    notification_title,
                    namespace,
                    name,
                )


def fetch_image_repos(access_token: str, namespace: str) -> Iterator[List[ImageRepo]]:
    """Fetch all image repositories for a given namespace"""
    next_page = None
    resp: HTTPResponse
    retry = 0
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
        try:
            with urlopen(request) as resp:
                if resp.status == 200:
                    json_data = json.loads(resp.read())
                else:
                    # this will raise error for 2xx other than 200
                    # urlopen raises HTTPError for all non 2xx responses
                    raise HTTPError(resp.reason)
        except HTTPError as ex:
            # retry 5 times before giving up
            if retry < 5:
                retry += 1
                continue
            else:
                LOGGER.error(
                    "Unable to fetch repositories for namespace %s",
                    namespace,
                )
                raise RuntimeError(ex)

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
