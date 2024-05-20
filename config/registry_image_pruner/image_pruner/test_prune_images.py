import json
import os
import re
import unittest
from email.message import Message
from typing import Final
from unittest.mock import call, patch, MagicMock
from urllib.parse import parse_qsl, urlparse
from urllib.request import Request
from urllib.error import HTTPError

from prune_images import fetch_image_repos, main, remove_tags, LOGGER, QUAY_API_URL

QUAY_TOKEN: Final = "1234"


class TestPruner(unittest.TestCase):

    def assert_make_get_request(self, request: Request) -> None:
        self.assertEqual("GET", request.get_method())

    def assert_quay_token_included(self, request: Request) -> None:
        self.assertEqual(f"Bearer {QUAY_TOKEN}", request.get_header("Authorization"))

    @patch.dict(os.environ, {"QUAY_TOKEN": QUAY_TOKEN})
    @patch("sys.argv", ["prune_images", "--namespace", "sample"])
    @patch("prune_images.urlopen")
    @patch("prune_images.get_quay_tags")
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
    @patch("prune_images.delete_image_tag")
    def test_no_image_with_expected_suffixes_is_found(self, delete_image_tag, urlopen):
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
            "tags": [
                {"name": "latest", "manifest_digest": "sha256:03fabe17d4c5"},
                {"name": "devel", "manifest_digest": "sha256:071c766795a0"},
            ],
        }).encode()
        get_repo_rv.__enter__.return_value = response

        urlopen.side_effect = [
            # yield repositories
            fetch_repos_rv,
            # return the repo info including tags
            get_repo_rv,
        ]

        main()

        delete_image_tag.assert_not_called()

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
        self.assertEqual(f"{QUAY_API_URL}/repository/sample/hello-image/tag/"
                         f"?limit=100&onlyActiveTags=True", request.get_full_url())
        self.assert_make_get_request(request)
        self.assert_quay_token_included(request)

    @patch.dict(os.environ, {"QUAY_TOKEN": QUAY_TOKEN})
    @patch("sys.argv", ["prune_images", "--namespace", "sample"])
    @patch("prune_images.urlopen")
    def test_remove_orphan_tags_with_expected_suffixes(self, urlopen):
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
            "tags": [
                {"name": "latest", "manifest_digest": "sha256:93a8743dc130"},
                # image manifest sha256:03fabe17d4c5 does not exist
                {
                    "name": "sha256-03fabe17d4c5.sbom",
                    "manifest_digest": "sha256:e45fad41f2ff",
                },
                {
                    "name": "sha256-03fabe17d4c5.att",
                    "manifest_digest": "sha256:e45fad41f2ff",
                },
                {
                    "name": "sha256-03fabe17d4c5.src",
                    "manifest_digest": "sha256:f490ad41f2cc",
                },
                # image manifest sha256:071c766795a0 does not exist
                {
                    "name": "sha256-071c766795a0.sbom",
                    "manifest_digest": "sha256:961207f62413",
                },
                {
                    "name": "sha256-071c766795a0.att",
                    "manifest_digest": "sha256:961207f62413",
                },
                {
                    "name": "sha256-071c766795a0.src",
                    "manifest_digest": "sha256:0ab207f62413",
                },

                # single deprecated source image tag remains
                {"name": "123abcd.src", "manifest_digest": "sha256:1234566"},

                # old image has only the deprecated source image, which should not be removed
                {"name": "donotdelete", "manifest_digest": "sha256:1234567"},
                {"name": "donotdelete.src", "manifest_digest": "sha256:1234568"},

                # the binary image was deleted before. Now, these should be deleted.
                {"name": "build-100.src", "manifest_digest": "sha256:1345678"},
                {"name": "sha256-4567890.src", "manifest_digest": "sha256:1345678"},

                # existent image has a source image tagged with a deprecated tag as well.
                # That should be removed.
                {"name": "1a2b3c4df", "manifest_digest": "sha256:1237890"},
                {"name": "1a2b3c4df.src", "manifest_digest": "sha256:2345678"},
                {"name": "sha256-1237890.src", "manifest_digest": "sha256:2345678"},
            ],
        }).encode()
        get_repo_rv.__enter__.return_value = response

        delete_tag_rv = MagicMock()
        response = MagicMock()
        response.status = 204
        delete_tag_rv.__enter__.return_value = response

        urlopen.side_effect = [
            # yield repositories
            fetch_repos_rv,
            # return the repo info including tags
            get_repo_rv,
            # return value for deleting tags
            delete_tag_rv,
            delete_tag_rv,
            delete_tag_rv,
            delete_tag_rv,
            delete_tag_rv,
            delete_tag_rv,

            delete_tag_rv,
            delete_tag_rv,
            delete_tag_rv,
            delete_tag_rv,
        ]

        main()

        def _assert_deletion_request(request: Request, tag: str) -> None:
            expected_url_path = f"{QUAY_API_URL}/repository/sample/hello-image/tag/{tag}"
            self.assertEqual(expected_url_path, request.get_full_url())
            self.assertEqual("DELETE", request.get_method())

        # keep same order as above
        tags_to_remove = (
            "sha256-03fabe17d4c5.sbom", "sha256-03fabe17d4c5.att", "sha256-03fabe17d4c5.src",
            "sha256-071c766795a0.sbom", "sha256-071c766795a0.att", "sha256-071c766795a0.src",
            "123abcd.src", "build-100.src", "sha256-4567890.src", "1a2b3c4df.src",
        )
        test_pairs = zip(tags_to_remove, urlopen.mock_calls[-10:])
        for tag, call in test_pairs:
            _assert_deletion_request(call.args[0], tag)

    @patch.dict(os.environ, {"QUAY_TOKEN": QUAY_TOKEN})
    @patch("sys.argv", ["prune_images", "--namespace", "sample", "--dry-run"])
    @patch("prune_images.urlopen")
    def test_remove_tag_dry_run(self, urlopen):
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
            "tags": [
                {"name": "latest", "manifest_digest": "sha256:93a8743dc130"},
                # dry run on this one
                {"name": "sha256-071c766795a0.sbom", "manifest_digest": "sha256:961207f62413"},
            ],
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
                if re.search(r"Tag sha256-071c766795a0.sbom from [^ /]+/[^ ]+ should be removed$", msg)
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


class TestRemoveTags(unittest.TestCase):

    @patch("prune_images.delete_image_tag")
    def test_remove_tags(self, delete_image_tag):
        tags = [
            {
                "name": "sha256-502c8c35e31459e8774f88e115d50d2ad33ba0e9dfd80429bc70ed4c1fd9e0cd.att",
                "manifest_digest": "sha256:125c1d18ee1c3b9bde0c7810fcb0d4ffbc67e9b0c5b88bb8df9ca039bc1c9457",
            },
            {
                "name": "sha256-502c8c35e31459e8774f88e115d50d2ad33ba0e9dfd80429bc70ed4c1fd9e0cd.sbom",
                "manifest_digest": "sha256:351326f899759a9a7ae3ca3c1cbdadcc8012f43231c145534820a68bdf36d55b",
            },
        ]

        with self.assertLogs(LOGGER) as logs:
            remove_tags(tags, QUAY_TOKEN, "some", "repository")
            logs_output = "\n".join(logs.output)
            for tag in tags:
                self.assertIn(f"Removing tag {tag['name']} from some/repository", logs_output)

        self.assertEqual(len(tags), delete_image_tag.call_count)
        calls = [call(QUAY_TOKEN, "some", "repository", tag['name']) for tag in tags]
        delete_image_tag.assert_has_calls(calls)

    @patch("prune_images.delete_image_tag")
    def test_remove_tags_dry_run(self, delete_image_tag):
        tags = [
            {
                "name": "sha256-502c8c35e31459e8774f88e115d50d2ad33ba0e9dfd80429bc70ed4c1fd9e0cd.att",
                "manifest_digest": "sha256:125c1d18ee1c3b9bde0c7810fcb0d4ffbc67e9b0c5b88bb8df9ca039bc1c9457",
            },
            {
                "name": "sha256-502c8c35e31459e8774f88e115d50d2ad33ba0e9dfd80429bc70ed4c1fd9e0cd.sbom",
                "manifest_digest": "sha256:351326f899759a9a7ae3ca3c1cbdadcc8012f43231c145534820a68bdf36d55b",
            }
        ]

        with self.assertLogs(LOGGER) as logs:
            remove_tags(tags, QUAY_TOKEN, "some", "repository", dry_run=True)
            logs_output = "\n".join(logs.output)
            for tag in tags:
                self.assertIn(f"Tag {tag['name']} from some/repository should be removed", logs_output)

        delete_image_tag.assert_not_called()

    @patch("prune_images.delete_image_tag")
    def test_remove_tags_nothing_to_remove(self, delete_image_tag):
        tags = [
            {
                "name": "sha256-502c8c35e31459e8774f88e115d50d2ad33ba0e9dfd80429bc70ed4c1fd9e0cd.att",
                "manifest_digest": "sha256:125c1d18ee1c3b9bde0c7810fcb0d4ffbc67e9b0c5b88bb8df9ca039bc1c9457",
            },
            {
                "name": "sha256-502c8c35e31459e8774f88e115d50d2ad33ba0e9dfd80429bc70ed4c1fd9e0cd.sbom",
                "manifest_digest": "sha256:351326f899759a9a7ae3ca3c1cbdadcc8012f43231c145534820a68bdf36d55b",
            },
            {
                "name": "app-image",
                "manifest_digest": "sha256:502c8c35e31459e8774f88e115d50d2ad33ba0e9dfd80429bc70ed4c1fd9e0cd",
            }
        ]

        with self.assertRaisesRegex(AssertionError, expected_regex="no logs of level INFO"):
            with self.assertLogs(LOGGER) as logs:
                remove_tags(tags, QUAY_TOKEN, "some", "repository", dry_run=True)

        delete_image_tag.assert_not_called()

    @patch("prune_images.delete_image_tag")
    def test_remove_tags_multiple_tags(self, delete_image_tag):
        tags = [
            {
                "name": "sha256-502c8c35e31459e8774f88e115d50d2ad33ba0e9dfd80429bc70ed4c1fd9e0cd.att",
                "manifest_digest": "sha256:125c1d18ee1c3b9bde0c7810fcb0d4ffbc67e9b0c5b88bb8df9ca039bc1c9457",
            },
            {
                "name": "sha256-502c8c35e31459e8774f88e115d50d2ad33ba0e9dfd80429bc70ed4c1fd9e0cd.sbom",
                "manifest_digest": "sha256:351326f899759a9a7ae3ca3c1cbdadcc8012f43231c145534820a68bdf36d55b",
            },
            {
                "name": "sha256-5c55025c0cfc402b2a42f9d35b14a92b1ba203407d2a81aad7ea3eae1a3737d4.att",
                "manifest_digest": "sha256:5126ed26d60fffab5f82783af65b5a8e69da0820b723eea82a0eb71b0743c191",
            },
            {
                "name": "sha256-5c55025c0cfc402b2a42f9d35b14a92b1ba203407d2a81aad7ea3eae1a3737d4.sbom",
                "manifest_digest": "sha256:9b1f70d94117c63ee73d53688a3e4d412c1ba58d86b8e45845cce9b8dab44113",
            },
        ]

        with self.assertLogs(LOGGER) as logs:
            remove_tags(tags, QUAY_TOKEN, "some", "repository")
            logs_output = "\n".join(logs.output)
            for tag in tags:
                self.assertIn(f"Removing tag {tag['name']} from some/repository", logs_output)

        self.assertEqual(len(tags), delete_image_tag.call_count)
        calls = [call(QUAY_TOKEN, "some", "repository", tag["name"]) for tag in tags]
        delete_image_tag.assert_has_calls(calls)


if __name__ == "__main__":
    unittest.main()
