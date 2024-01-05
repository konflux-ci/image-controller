import json
import os
import re
import unittest
from email.message import Message
from typing import Final
from unittest.mock import patch, MagicMock
from urllib.parse import parse_qsl, urlparse
from urllib.request import Request
from urllib.error import HTTPError

from prune_images import fetch_image_repos, main, LOGGER, QUAY_API_URL

QUAY_TOKEN: Final = "1234"


class TestPruner(unittest.TestCase):

    def assert_make_get_request(self, request: Request) -> None:
        self.assertEqual("GET", request.get_method())

    def assert_quay_token_included(self, request: Request) -> None:
        self.assertEqual(f"Bearer {QUAY_TOKEN}", request.get_header("Authorization"))

    @patch.dict(os.environ, {"QUAY_TOKEN": QUAY_TOKEN})
    @patch("sys.argv", ["prune_images", "--namespace", "sample"])
    @patch("prune_images.urlopen")
    @patch("prune_images.get_quay_repo")
    def test_no_image_repo_is_fetched(self, get_quay_repo, urlopen):
        response = MagicMock()
        response.status = 200
        response.read.return_value = b"{}"
        urlopen.return_value.__enter__.return_value = response

        main()

        get_quay_repo.assert_not_called()

    @patch.dict(os.environ, {"QUAY_TOKEN": QUAY_TOKEN})
    @patch("sys.argv", ["prune_images", "--namespace", "sample"])
    @patch("prune_images.urlopen")
    @patch("prune_images.delete_image_repo")
    def test_no_image_with_expected_suffixes_is_found(self, delete_image_repo, urlopen):
        fetch_repos_rv = MagicMock()
        response = MagicMock()
        response.status = 200
        response.read.return_value = json.dumps({
            "repositories": [
                {"namespace": "sample", "name": "hello-image"},
            ],
        }).encode()
        fetch_repos_rv.__enter__.return_value = response

        get_repo_rv = MagicMock()
        response = MagicMock()
        response.status = 200
        # no .att or .sbom suffix here
        response.read.return_value = json.dumps({
            "tags": {
                "latest": {"name": "latest", "manifest_digest": "sha256:03fabe17d4c5"},
                "devel": {"name": "devel", "manifest_digest": "sha256:071c766795a0"},
            },
        }).encode()
        get_repo_rv.__enter__.return_value = response

        urlopen.side_effect = [
            # yield repositories
            fetch_repos_rv,
            # return the repo info including tags
            get_repo_rv,
        ]

        main()

        delete_image_repo.assert_not_called()

        self.assertEqual(2, urlopen.call_count)

        fetch_repos_call = urlopen.mock_calls[0]
        request: Request = fetch_repos_call.args[0]
        parsed_url = urlparse(request.get_full_url())
        self.assertEqual(
            f"{QUAY_API_URL}/repository",
            f"{parsed_url.scheme}://{parsed_url.netloc}{parsed_url.path}",
        )
        self.assertDictEqual({"namespace": "sample"}, dict(parse_qsl(parsed_url.query)))

        self.assert_make_get_request(request)
        self.assert_quay_token_included(request)

        get_repo_call = urlopen.mock_calls[1]
        request: Request = get_repo_call.args[0]
        self.assertEqual(f"{QUAY_API_URL}/repository/sample/hello-image", request.get_full_url())
        self.assert_make_get_request(request)
        self.assert_quay_token_included(request)

    @patch.dict(os.environ, {"QUAY_TOKEN": QUAY_TOKEN})
    @patch("sys.argv", ["prune_images", "--namespace", "sample"])
    @patch("prune_images.urlopen")
    def test_remove_orphan_images_with_expected_suffixes(self, urlopen):
        fetch_repos_rv = MagicMock()
        response = MagicMock()
        response.status = 200
        response.read.return_value = json.dumps({
            "repositories": [
                {"namespace": "sample", "name": "hello-image"},
            ],
        }).encode()
        fetch_repos_rv.__enter__.return_value = response

        get_repo_rv = MagicMock()
        response = MagicMock()
        response.status = 200
        # no .att or .sbom suffix here
        response.read.return_value = json.dumps({
            "tags": {
                "latest": {"name": "latest", "manifest_digest": "sha256:93a8743dc130"},
                # image sha256:e45fad41f2ff has been removed
                "sha256-03fabe17d4c5.sbom": {
                    "name": "sha256-03fabe17d4c5.sbom",
                    "manifest_digest": "sha256:e45fad41f2ff",
                },
                "sha256-03fabe17d4c5.att": {
                    "name": "sha256-03fabe17d4c5.att",
                    "manifest_digest": "sha256:e45fad41f2ff",
                },
                # image sha256:03fabe17d4c5 has been removed
                "sha256-071c766795a0.sbom": {
                    "name": "sha256-071c766795a0.sbom",
                    "manifest_digest": "sha256:961207f62413",
                },
                "sha256-071c766795a0.att": {
                    "name": "sha256-071c766795a0.att",
                    "manifest_digest": "sha256:961207f62413",
                },
            },
        }).encode()
        get_repo_rv.__enter__.return_value = response

        delete_image_rv = MagicMock()
        response = MagicMock()
        response.status = 204
        delete_image_rv.__enter__.return_value = response

        urlopen.side_effect = [
            # yield repositories
            fetch_repos_rv,
            # return the repo info including tags
            get_repo_rv,
            # return value for deleting images
            delete_image_rv,
            delete_image_rv,
            delete_image_rv,
            delete_image_rv,
        ]

        main()

        self.assertEqual(6, urlopen.call_count)

        def _assert_deletion_request(request: Request, tag: str) -> None:
            expected_url_path = f"{QUAY_API_URL}/repository/sample/hello-image/tag/{tag}"
            self.assertEqual(expected_url_path, request.get_full_url())
            self.assertEqual("DELETE", request.get_method())

        test_pairs = zip(
            # keep same order as above
            ("sha256-03fabe17d4c5.sbom", "sha256-03fabe17d4c5.att",
             "sha256-071c766795a0.sbom", "sha256-071c766795a0.att"),
            urlopen.mock_calls[-4:],
        )
        for tag, call in test_pairs:
            _assert_deletion_request(call.args[0], tag)

    @patch.dict(os.environ, {"QUAY_TOKEN": QUAY_TOKEN})
    @patch("sys.argv", ["prune_images", "--namespace", "sample", "--dry-run"])
    @patch("prune_images.urlopen")
    def test_remove_image_dry_run(self, urlopen):
        fetch_repos_rv = MagicMock()
        response = MagicMock()
        response.status = 200
        response.read.return_value = json.dumps({
            "repositories": [
                {"namespace": "sample", "name": "hello-image"},
            ],
        }).encode()
        fetch_repos_rv.__enter__.return_value = response

        get_repo_rv = MagicMock()
        response = MagicMock()
        response.status = 200
        response.read.return_value = json.dumps({
            "tags": {
                "latest": {"manifest_digest": "sha256:93a8743dc130"},
                # dry run on this one
                "sha256-071c766795a0.sbom": {
                    "name": "sha256-071c766795a0.sbom",
                    "manifest_digest": "sha256:961207f62413",
                },
            },
        }).encode()
        get_repo_rv.__enter__.return_value = response

        urlopen.side_effect = [
            # yield repositories
            fetch_repos_rv,
            # return the repo info including tags
            get_repo_rv,
        ]

        with self.assertLogs(LOGGER) as logs:
            main()
            dry_run_log = [
                msg for msg in logs.output
                if re.search(r"Image sha256-071c766795a0.sbom from [^ /]+/[^ ]+ should be removed$", msg)
            ]
            self.assertEqual(1, len(dry_run_log))

        self.assertEqual(2, urlopen.call_count)

    @patch.dict(os.environ, {"QUAY_TOKEN": QUAY_TOKEN})
    @patch("sys.argv", ["prune_images", "--namespace", "sample"])
    @patch("prune_images.urlopen")
    def test_crash_when_http_error(self, urlopen):
        urlopen.side_effect = HTTPError("url", 404, "something is not found", Message(), None)
        with self.assertRaises(HTTPError):
            main()

    @patch("sys.argv", ["prune_images", "--namespace", "sample"])
    def test_missing_quay_token_in_env(self):
        with self.assertRaisesRegex(ValueError, r"The token .+ is missing"):
            main()

    @patch("prune_images.urlopen")
    def test_handle_image_repos_pagination(self, urlopen):
        first_fetch_rv = MagicMock()
        response = MagicMock()
        response.status = 200
        response.read.return_value = json.dumps({
            "repositories": [
                {"namespace": "sample", "name": "hello-image"},
            ],
            "next_page": 2,
        }).encode()
        first_fetch_rv.__enter__.return_value = response

        second_fetch_rv = MagicMock()
        response = MagicMock()
        response.status = 200
        # no next_page is included
        response.read.return_value = json.dumps({
            "repositories": [
                {"namespace": "sample", "name": "another-image"},
            ],
        }).encode()
        second_fetch_rv.__enter__.return_value = response

        urlopen.side_effect = [first_fetch_rv, second_fetch_rv]

        fetcher = fetch_image_repos(QUAY_TOKEN, "sample")

        self.assertListEqual([{"namespace": "sample", "name": "hello-image"}], next(fetcher))
        self.assertListEqual([{"namespace": "sample", "name": "another-image"}], next(fetcher))
        with self.assertRaises(StopIteration):
            next(fetcher)


if __name__ == "__main__":
    unittest.main()
