import json
import os
import unittest

from datetime import datetime, UTC
from email.message import Message
from typing import Final
from unittest.mock import call, patch, MagicMock
from urllib.parse import parse_qsl, urlparse
from urllib.request import Request
from urllib.error import HTTPError

from prune_images import TimeRange, ARTIFACTS_TAGS_SUFFIX, SubjectImageDigestCollector
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
        response.read.return_value = json.dumps(
            {
                "repositories": [
                    {"namespace": "sample", "name": "hello-image"},
                ],
            }
        ).encode()
        fetch_repos_rv.__enter__.return_value = response

        get_repo_rv = MagicMock()
        response = MagicMock()
        response.status = 200
        # no .att or .sbom suffix here
        response.read.return_value = json.dumps(
            {
                "tags": [
                    {"name": "latest", "manifest_digest": "sha256:03fabe17d4c5", "start_ts": 1000},
                    {"name": "devel", "manifest_digest": "sha256:071c766795a0", "start_ts": 900},
                ],
            }
        ).encode()
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
        self.assertEqual(
            f"{QUAY_API_URL}/repository/sample/hello-image/tag/?limit=100&onlyActiveTags=True&page=1",
            request.get_full_url(),
        )
        self.assert_make_get_request(request)
        self.assert_quay_token_included(request)

    @patch.dict(os.environ, {"QUAY_TOKEN": QUAY_TOKEN})
    @patch("sys.argv", ["prune_images", "--namespace", "sample"])
    @patch("prune_images.urlopen")
    @patch("prune_images.manifest_exists")
    def test_remove_orphan_tags_with_expected_suffixes(self, manifest_exists, urlopen):
        fetch_repos_rv = MagicMock()
        response = MagicMock()
        response.status = 200
        response.read.return_value = json.dumps(
            {
                "repositories": [
                    {"namespace": "sample", "name": "hello-image"},
                ],
            }
        ).encode()
        fetch_repos_rv.__enter__.return_value = response

        quay_tags = {
            "tags": [
                {"name": "latest", "manifest_digest": "sha256:93a8743dc130", "start_ts": 1000},
                # manifest sha256:502c8c35e31459e8774f88e115d50d2ad33ba0e9dfd80429bc70ed4c1fd9e0cd does not exist
                {
                    "name": "sha256-502c8c35e31459e8774f88e115d50d2ad33ba0e9dfd80429bc70ed4c1fd9e0cd.sbom",
                    "manifest_digest": "sha256:125c1d18ee1c3b9bde0c7810fcb0d4ffbc67e9b0c5b88bb8df9ca039bc1c9457",
                    "start_ts": 900,
                },
                {
                    "name": "sha256-502c8c35e31459e8774f88e115d50d2ad33ba0e9dfd80429bc70ed4c1fd9e0cd.att",
                    "manifest_digest": "sha256:789c1d18ee1c3b9bde0c7810fcb0d4ffbc67e9b0c5b88bb8df9ca039bc1c9457",
                    "start_ts": 800,
                },
                {
                    "name": "sha256-502c8c35e31459e8774f88e115d50d2ad33ba0e9dfd80429bc70ed4c1fd9e0cd.src",
                    "manifest_digest": "sha256:f490ad41f2cc789c1d18ee1c3b9bde0c7810fcb0d4ffbc67e9b0c5b88bb8df9c",
                    "start_ts": 700,
                },
                # manifest sha256:5c55025c0cfc402b2a42f9d35b14a92b1ba203407d2a81aad7ea3eae1a3737d4 does not exist
                {
                    "name": "sha256-5c55025c0cfc402b2a42f9d35b14a92b1ba203407d2a81aad7ea3eae1a3737d4.sbom",
                    "manifest_digest": "sha256:961207f62413f490ad41f2cc789c1d18ee1c3b9bde0c7810fcb0d4ffbc67e9b0",
                    "start_ts": 600,
                },
                {
                    "name": "sha256-5c55025c0cfc402b2a42f9d35b14a92b1ba203407d2a81aad7ea3eae1a3737d4.att",
                    "manifest_digest": "sha256:96961207f62413f490ad41f2cc789c1d18ee1c3b9bde0c7810fcb0d4ffbc67e9",
                    "start_ts": 500,
                },
                {
                    "name": "sha256-5c55025c0cfc402b2a42f9d35b14a92b1ba203407d2a81aad7ea3eae1a3737d4.src",
                    "manifest_digest": "sha256:0ab2096961207f62413f490ad41f2cc789c1d18ee1c3b9bde0c7810fcb0d4ffb",
                    "start_ts": 400,
                },
            ],
        }
        subject_image_digests = set([tag["name"].split(".")[0] for tag in quay_tags["tags"][1:]])
        number_of_tag_deletions = len(subject_image_digests) * len(ARTIFACTS_TAGS_SUFFIX)

        get_repo_rv = MagicMock()
        response = MagicMock()
        response.status = 200
        response.read.return_value = json.dumps(quay_tags).encode()
        get_repo_rv.__enter__.return_value = response

        delete_tag_rv = MagicMock()
        response = MagicMock()
        response.status = 204
        delete_tag_rv.__enter__.return_value = response

        side_effects = [
            # yield repositories
            fetch_repos_rv,
            # return the repo info including tags
            get_repo_rv,
        ]
        # return value for deleting tags
        side_effects.extend([delete_tag_rv] * number_of_tag_deletions)
        urlopen.side_effect = side_effects

        manifest_exists.side_effect = [False, False, False, False, False, False]

        main()

        # keep same order as above
        tags_to_remove = [digest + suffix for digest in subject_image_digests for suffix in ARTIFACTS_TAGS_SUFFIX]

        for urlopen_call in urlopen.mock_calls[2:]:
            arg_request = urlopen_call.args[0]
            self.assertEqual(arg_request.get_method(), "DELETE")
            self.assertIn(os.path.basename(arg_request.get_full_url()), tags_to_remove)

    @patch.dict(os.environ, {"QUAY_TOKEN": QUAY_TOKEN})
    @patch("sys.argv", ["prune_images", "--namespace", "sample", "--dry-run"])
    @patch("prune_images.urlopen")
    @patch("prune_images.manifest_exists")
    def test_remove_tag_dry_run(self, manifest_exists, urlopen):
        fetch_repos_rv = MagicMock()
        response = MagicMock()
        response.status = 200
        response.read.return_value = json.dumps(
            {
                "repositories": [
                    {"namespace": "sample", "name": "hello-image"},
                ],
            }
        ).encode()
        fetch_repos_rv.__enter__.return_value = response

        get_repo_rv = MagicMock()
        response = MagicMock()
        response.status = 200
        response.read.return_value = json.dumps(
            {
                "tags": [
                    {"name": "latest", "manifest_digest": "sha256:93a8743dc130", "start_ts": 1000},
                    # dry run on this one
                    {
                        "name": "sha256-502c8c35e31459e8774f88e115d50d2ad33ba0e9dfd80429bc70ed4c1fd9e0cd.sbom",
                        "manifest_digest": "sha256:351326f899759a9a7ae3ca3c1cbdadcc8012f43231c145534820a68bdf36d55b",
                        "start_ts": 999,
                    },
                ],
            }
        ).encode()
        get_repo_rv.__enter__.return_value = response

        urlopen.side_effect = [
            # yield repositories
            fetch_repos_rv,
            # return the repo info including tags
            get_repo_rv,
        ]
        manifest_exists.side_effect = [
            False,
        ]

        with self.assertLogs(LOGGER) as logs:
            main()

            log_text = "\n".join(logs.output)
            self.assertIn(
                "Tag sha256-502c8c35e31459e8774f88e115d50d2ad33ba0e9dfd80429bc70ed4c1fd9e0cd.sbom from "
                "sample/hello-image should be removed",
                log_text,
            )

        self.assertEqual(2, urlopen.call_count)

    @patch.dict(os.environ, {"QUAY_TOKEN": QUAY_TOKEN})
    @patch("sys.argv", ["prune_images", "--namespace", "sample"])
    @patch("prune_images.urlopen")
    def test_crash_when_http_error(self, urlopen):
        urlopen.side_effect = HTTPError("url", 404, "something is not found", Message(), None)
        with self.assertRaises(HTTPError):
            main()

    @patch("sys.argv", ["prune_images", "--namespace", "sample"])
    @patch.object(os, "environ", new={})
    def test_missing_quay_token_in_env(self):
        with self.assertRaisesRegex(ValueError, r"The token .+ is missing"):
            main()

    @patch("prune_images.urlopen")
    def test_handle_image_repos_pagination(self, urlopen):
        first_fetch_rv = MagicMock()
        response = MagicMock()
        response.status = 200
        response.read.return_value = json.dumps(
            {
                "repositories": [
                    {"namespace": "sample", "name": "hello-image"},
                ],
                "next_page": 2,
            }
        ).encode()
        first_fetch_rv.__enter__.return_value = response

        second_fetch_rv = MagicMock()
        response = MagicMock()
        response.status = 200
        # no next_page is included
        response.read.return_value = json.dumps(
            {
                "repositories": [
                    {"namespace": "sample", "name": "another-image"},
                ],
            }
        ).encode()
        second_fetch_rv.__enter__.return_value = response

        urlopen.side_effect = [first_fetch_rv, second_fetch_rv]

        fetcher = fetch_image_repos(QUAY_TOKEN, "sample")

        self.assertListEqual([{"namespace": "sample", "name": "hello-image"}], next(fetcher))
        self.assertListEqual([{"namespace": "sample", "name": "another-image"}], next(fetcher))
        with self.assertRaises(StopIteration):
            next(fetcher)


class TestRemoveTags(unittest.TestCase):

    @patch("prune_images.delete_image_tag")
    @patch("prune_images.manifest_exists")
    def test_remove_tags(self, manifest_exists, delete_image_tag):
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

        manifest_exists.side_effect = [False, False]

        subject_image_digests = [
            tag["name"].removesuffix(".att").removesuffix(".sbom").replace("-", ":") for tag in tags
        ]

        with self.assertLogs(LOGGER) as logs:
            remove_tags(subject_image_digests, QUAY_TOKEN, "some", "repository")
            logs_output = "\n".join(logs.output)
            for tag in tags:
                self.assertRegex(logs_output, rf"Removing tag \d+: {tag['name']} from some/repository")

        self.assertEqual(manifest_exists.call_count, 2)
        manifest_exists.assert_has_calls(
            [call(QUAY_TOKEN, "some", "repository", digest) for digest in subject_image_digests]
        )

        self.assertEqual(len(subject_image_digests) * len(ARTIFACTS_TAGS_SUFFIX), delete_image_tag.call_count)
        calls = [
            call(QUAY_TOKEN, "some", "repository", digest.replace(":", "-") + suffix)
            for digest in subject_image_digests
            for suffix in ARTIFACTS_TAGS_SUFFIX
        ]
        delete_image_tag.assert_has_calls(calls)

    @patch("prune_images.delete_image_tag")
    @patch("prune_images.manifest_exists")
    def test_remove_tags_dry_run(self, manifest_exists, delete_image_tag):
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

        manifest_exists.side_effect = [False, False]

        subject_image_digests = [
            tag["name"].removesuffix(".att").removesuffix(".sbom").replace("-", ":") for tag in tags
        ]

        with self.assertLogs(LOGGER) as logs:
            remove_tags(subject_image_digests, QUAY_TOKEN, "some", "repository", dry_run=True)
            logs_output = "\n".join(logs.output)
            for tag in tags:
                self.assertIn(f"Tag {tag['name']} from some/repository should be removed", logs_output)

        delete_image_tag.assert_not_called()

    @patch("prune_images.delete_image_tag")
    @patch("prune_images.manifest_exists")
    def test_remove_tags_nothing_to_remove_digest_exists(self, manifest_exists, delete_image_tag):
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

        manifest_exists.side_effect = [True, True]

        subject_image_digests = [
            tag["name"].removesuffix(".att").removesuffix(".sbom").replace("-", ":") for tag in tags
        ]

        with self.assertLogs(LOGGER) as logs:
            remove_tags(subject_image_digests, QUAY_TOKEN, "some", "repository", dry_run=True)
            log_text = "\n".join(logs.output)
            for digest in subject_image_digests:
                self.assertIn(f"Image manifest still exists: {digest}", log_text)

        delete_image_tag.assert_not_called()

    @patch("prune_images.delete_image_tag")
    @patch("prune_images.manifest_exists")
    def test_remove_tags_multiple_tags(self, manifest_exists, delete_image_tag):
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

        manifest_exists.side_effect = [False, False, False, False]

        subject_image_digests = list(
            set([tag["name"].removesuffix(".att").removesuffix(".sbom").replace("-", ":") for tag in tags])
        )

        with self.assertLogs(LOGGER) as logs:
            remove_tags(subject_image_digests, QUAY_TOKEN, "some", "repository")
            logs_output = "\n".join(logs.output)
            for tag in tags:
                self.assertRegex(logs_output, rf"Removing tag \d+: {tag['name']} from some/repository")

        self.assertEqual(len(subject_image_digests) * len(ARTIFACTS_TAGS_SUFFIX), delete_image_tag.call_count)
        calls = [
            call(QUAY_TOKEN, "some", "repository", digest.replace(":", "-") + suffix)
            for digest in subject_image_digests
            for suffix in ARTIFACTS_TAGS_SUFFIX
        ]
        delete_image_tag.assert_has_calls(calls)


def test_time_range_past_days():
    since = datetime.now(UTC)
    tr = TimeRange.past_days(2, since=since)
    assert tr.from_ts == since.timestamp()
    to_dt = datetime.fromtimestamp(tr.to_ts, UTC)
    assert (since - to_dt).days == 2


class TestSubjectImageDigestCollector:

    tags_list = {
        "tags": [
            {
                "name": "sha256-502c8c35e31459e8774f88e115d50d2ad33ba0e9dfd80429bc70ed4c1fd9e0cd.sbom",
                "manifest_digest": "sha256:125c1d18ee1c3b9bde0c7810fcb0d4ffbc67e9b0c5b88bb8df9ca039bc1c9457",
                "start_ts": 1000,
            },
            {
                "name": "sha256-502c8c35e31459e8774f88e115d50d2ad33ba0e9dfd80429bc70ed4c1fd9e0cd.att",
                "manifest_digest": "sha256:789c1d18ee1c3b9bde0c7810fcb0d4ffbc67e9b0c5b88bb8df9ca039bc1c9457",
                "start_ts": 950,
            },
            {
                "name": "sha256-8c35e31459e8774f88e115d50d2ad33ba0e9dfd80429bc70ed4c1fd9e0cd3145",
                "manifest_digest": "sha256:502c8c35e31459e8774f88e115d50d2ad33ba0e9dfd80429bc70ed4c1fd9e0cd",
                "start_ts": 800,
            },
            {
                "name": "sha256-774f88e115d50d2ad33ba0e9dfd80429bc70ed4c1fd9e0cd2c8c35e31459e87a.sbom",
                "manifest_digest": "sha256:89c1d18ee1c3b9bde0c7810fcb0d4ffbc67e9b0c5b88bb8df9ca039bc1c9457d",
                "start_ts": 750,
            },
            {
                "name": "sha256-774f88e115d50d2ad33ba0e9dfd80429bc70ed4c1fd9e0cd2c8c35e31459e87a.att",
                "manifest_digest": "sha256:1d18ee1c3b9bde0c7810fcb0d4ffbc67e9b0c5b88bb8df9ca039bc1c9457dd18",
                "start_ts": 600,
            },
        ],
    }

    def test_collect(self):
        collector = SubjectImageDigestCollector()
        json.loads(json.dumps(self.tags_list), object_pairs_hook=collector)

        expected = ["sha256:774f88e115d50d2ad33ba0e9dfd80429bc70ed4c1fd9e0cd2c8c35e31459e87a"]
        assert collector.image_digests == expected

    def test_collect_with_time_range(self):
        tr = TimeRange(from_ts=1000, to_ts=980)
        collector = SubjectImageDigestCollector(tr)
        json_data = json.loads(json.dumps(self.tags_list), object_pairs_hook=collector)

        expected = ["sha256:502c8c35e31459e8774f88e115d50d2ad33ba0e9dfd80429bc70ed4c1fd9e0cd"]
        assert collector.image_digests == expected

        assert json_data["out_of_time_range"]


if __name__ == "__main__":
    unittest.main()
