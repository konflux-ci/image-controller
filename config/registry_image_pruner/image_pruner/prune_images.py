import argparse
import os
import logging
import re
import requests

logging.basicConfig(
    format="%(asctime)s - %(levelname)s - %(message)s", level=logging.INFO
)
LOGGER = logging.getLogger(__name__)
QUAY_API_URL = "https://quay.io/api/v1"

PROCESSED_REPOS = 0


def remove_images(images, session, repository, dry_run=False):
    image_digests = [image["manifest_digest"] for image in images.values()]
    for image in images:
        # attribute or sbom image
        if image.endswith(".att") or image.endswith(".sbom"):
            sha = re.search("sha256-(.*)(.sbom|.att)", image).group(1)
            if f"sha256:{sha}" not in image_digests:
                if dry_run:
                    LOGGER.info("Image %s from %s should be removed", image, repository)
                else:
                    LOGGER.info("Removing image %s from %s", image, repository)
                    delete_image_endpoint = (
                        f"{QUAY_API_URL}/repository/{repository}/tag/{image}"
                    )
                    response = session.delete(delete_image_endpoint)
                    response.raise_for_status()


def process_repositories(repositories, session, dry_run=False):
    global PROCESSED_REPOS

    for repo in repositories:
        repository = f"{repo['namespace']}/{repo['name']}"

        PROCESSED_REPOS += 1
        LOGGER.info("Processing repository %s: %s", PROCESSED_REPOS, repository)

        repository_endpoint = f"{QUAY_API_URL}/repository/{repository}"
        response = session.get(repository_endpoint)
        response.raise_for_status()
        repository_json = response.json()

        images = repository_json.get("tags")
        if images:
            remove_images(images, session, repository, dry_run=dry_run)


def main():
    token = os.getenv("QUAY_TOKEN")
    if not token:
        raise ValueError("The token required for access to Quay API is missing!")

    args = parse_args()

    session = requests.Session()
    session.headers = {"Authorization": f"Bearer {token}"}
    session.params = {"namespace": args.namespace}
    repositories_endpoint = f"{QUAY_API_URL}/repository"

    response = session.get(repositories_endpoint)
    response.raise_for_status()
    resp_json = response.json()

    repositories = resp_json.get("repositories")
    next_page = resp_json.get("next_page")

    if repositories:
        process_repositories(repositories, session, dry_run=args.dry_run)

    while next_page:
        response = session.get(repositories_endpoint, params={"next_page": next_page})
        response.raise_for_status()
        resp_json = response.json()

        repositories = resp_json.get("repositories")
        next_page = resp_json.get("next_page")
        process_repositories(repositories, session, dry_run=args.dry_run)


def parse_args():
    parser = argparse.ArgumentParser()
    parser.add_argument("--namespace", required=True)
    parser.add_argument("--dry-run", action="store_true")
    args = parser.parse_args()
    return args


if __name__ == "__main__":
    main()
