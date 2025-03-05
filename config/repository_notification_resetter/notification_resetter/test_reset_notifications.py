import io
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

from reset_notifications import (
    fetch_image_repos,
    main,
    LOGGER,
    QUAY_API_URL,
)

QUAY_TOKEN: Final = "1234"


class TestResetter(unittest.TestCase):

    def assert_make_get_request(self, request: Request) -> None:
        self.assertEqual("GET", request.get_method())

    def assert_make_post_request(self, request: Request) -> None:
        self.assertEqual("POST", request.get_method())

    def assert_quay_token_included(self, request: Request) -> None:
        self.assertEqual(f"Bearer {QUAY_TOKEN}", request.get_header("Authorization"))

    @patch.dict(os.environ, {"QUAY_TOKEN": QUAY_TOKEN})
    @patch("sys.argv", ["reset_notifications", "--namespace", "sample"])
    @patch("reset_notifications.urlopen")
    def test_crash_when_http_error(self, urlopen):
        urlopen.side_effect = HTTPError(
            "url", 404, "something is not found", Message(), None
        )
        with self.assertRaises(HTTPError):
            main()

    @patch.dict(os.environ, {"QUAY_TOKEN": QUAY_TOKEN})
    @patch("sys.argv", ["reset_notifications", "--namespace", "sample"])
    @patch("reset_notifications.urlopen")
    def test_pass_when_notification_not_exist(self, urlopen):
        fetch_repos = MagicMock()
        response = MagicMock()
        response.status = 200
        response.read.return_value = json.dumps(
            {
                "repositories": [
                    {"namespace": "sample", "name": "hello-image"},
                ],
            }
        ).encode()
        fetch_repos.__enter__.return_value = response

        get_notifications = MagicMock()
        response = MagicMock()
        response.status = 200
        response.read.return_value = json.dumps(
            {
                "notifications": [
                    {"uuid": "bar", "number_of_failures": 2},
                ],
            }
        ).encode()
        get_notifications.__enter__.return_value = response

        error_response = io.BytesIO(
            b'{"detail": "No repository notification found for: namespace, repo, uuid"}'
        )
        mock_error = HTTPError(
            url="http://example.com",
            code=400,
            msg="Bad Request",
            hdrs=None,
            fp=error_response,
        )

        urlopen.side_effect = [
            # yield repositories
            fetch_repos,
            # return repo notifications
            get_notifications,
            # reset notification error response
            mock_error,
        ]
        with self.assertLogs(LOGGER) as logs:
            main()
            run_log = [
                msg
                for msg in logs.output
                if re.search(
                    r"Notification .+ was not found",
                    msg,
                )
            ]
            self.assertEqual(1, len(run_log))

    @patch.dict(os.environ, {"QUAY_TOKEN": QUAY_TOKEN})
    @patch("sys.argv", ["reset_notifications", "--namespace", "sample"])
    @patch("reset_notifications.urlopen")
    def test_fail_when_notification_reset_fail(self, urlopen):
        fetch_repos = MagicMock()
        response = MagicMock()
        response.status = 200
        response.read.return_value = json.dumps(
            {
                "repositories": [
                    {"namespace": "sample", "name": "hello-image"},
                ],
            }
        ).encode()
        fetch_repos.__enter__.return_value = response

        get_notifications = MagicMock()
        response = MagicMock()
        response.status = 200
        response.read.return_value = json.dumps(
            {
                "notifications": [
                    {"uuid": "bar", "number_of_failures": 2},
                ],
            }
        ).encode()
        get_notifications.__enter__.return_value = response

        error_response = io.BytesIO(b'{"error": "Something is bad"}')
        mock_error = HTTPError(
            url="http://example.com",
            code=400,
            msg="Bad Request",
            hdrs=None,
            fp=error_response,
        )

        urlopen.side_effect = [
            # yield repositories
            fetch_repos,
            # return repo notifications
            get_notifications,
            # reset notification error response
            mock_error,
        ]
        with self.assertRaises(HTTPError):
            main()

    @patch("sys.argv", ["reset_notifications", "--namespace", "sample"])
    def test_missing_quay_token_in_env(self):
        with self.assertRaisesRegex(ValueError, r"^The token .+ is missing!$"):
            main()

    @patch.dict(os.environ, {"QUAY_TOKEN": QUAY_TOKEN})
    @patch("sys.argv", ["reset_notifications", "--namespace", "sample"])
    @patch("reset_notifications.urlopen")
    @patch("reset_notifications.get_quay_notifications")
    def test_no_image_repo_is_fetched(self, get_quay_notifications, urlopen):
        response = MagicMock()
        response.status = 200
        response.read.return_value = b"{}"
        urlopen.return_value.__enter__.return_value = response

        main()

        get_quay_notifications.assert_not_called()

    @patch.dict(os.environ, {"QUAY_TOKEN": QUAY_TOKEN})
    @patch("sys.argv", ["reset_notifications", "--namespace", "sample", "--dry-run"])
    @patch("reset_notifications.urlopen")
    @patch("reset_notifications.reset_notification")
    def test_reset_notification_dry_run(self, reset_notification, urlopen):
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

        get_notifications_rv = MagicMock()
        response = MagicMock()
        response.status = 200
        response.read.return_value = json.dumps(
            {
                "notifications": [
                    # only one notification with failures
                    {"uuid": "foo", "number_of_failures": 0},
                    {"uuid": "bar", "number_of_failures": 2},
                ],
            }
        ).encode()
        get_notifications_rv.__enter__.return_value = response

        urlopen.side_effect = [
            # yield repositories
            fetch_repos_rv,
            # return repo notifications
            get_notifications_rv,
        ]

        with self.assertLogs(LOGGER) as logs:
            main()
            dry_run_log = [
                msg
                for msg in logs.output
                if re.search(
                    r"Notification bar .+ should be reset",
                    msg,
                )
            ]
            self.assertEqual(1, len(dry_run_log))

        self.assertEqual(2, urlopen.call_count)
        reset_notification.assert_not_called()

        # get repositories for namespace call
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

        # get notifications for repo call
        get_notifications_call = urlopen.mock_calls[1]
        request: Request = get_notifications_call.args[0]
        self.assertEqual(
            f"{QUAY_API_URL}/repository/sample/hello-image/notification/",
            request.get_full_url(),
        )
        self.assert_make_get_request(request)
        self.assert_quay_token_included(request)

    @patch.dict(os.environ, {"QUAY_TOKEN": QUAY_TOKEN})
    @patch("sys.argv", ["reset_notifications", "--namespace", "sample"])
    @patch("reset_notifications.urlopen")
    def test_reset_notification(self, urlopen):
        fetch_repos = MagicMock()
        response = MagicMock()
        response.status = 200
        response.read.return_value = json.dumps(
            {
                "repositories": [
                    {"namespace": "sample", "name": "hello-image"},
                ],
            }
        ).encode()
        fetch_repos.__enter__.return_value = response

        get_notifications = MagicMock()
        response = MagicMock()
        response.status = 200
        response.read.return_value = json.dumps(
            {
                "notifications": [
                    # only one notification with failures
                    {"uuid": "foo", "number_of_failures": 0},
                    {"uuid": "bar", "number_of_failures": 2},
                ],
            }
        ).encode()
        get_notifications.__enter__.return_value = response

        reset_notification = MagicMock()
        response = MagicMock()
        response.status = 204
        reset_notification.__enter__.return_value = response

        urlopen.side_effect = [
            # yield repositories
            fetch_repos,
            # return repo notifications
            get_notifications,
            # reset notification
            reset_notification,
        ]

        with self.assertLogs(LOGGER) as logs:
            main()
            dry_run_log = [
                msg
                for msg in logs.output
                if re.search(
                    r"Notification bar .+ was reset",
                    msg,
                )
            ]
            self.assertEqual(1, len(dry_run_log))

        self.assertEqual(3, urlopen.call_count)

        # get repositories for namespace call
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

        # get notifications for repo call
        get_notifications_call = urlopen.mock_calls[1]
        request: Request = get_notifications_call.args[0]
        self.assertEqual(
            f"{QUAY_API_URL}/repository/sample/hello-image/notification/",
            request.get_full_url(),
        )
        self.assert_make_get_request(request)
        self.assert_quay_token_included(request)

        # reset notification call
        reset_notification_call = urlopen.mock_calls[2]
        request: Request = reset_notification_call.args[0]
        self.assertEqual(
            f"{QUAY_API_URL}/repository/sample/hello-image/notification/bar",
            request.get_full_url(),
        )
        self.assert_make_post_request(request)
        self.assert_quay_token_included(request)

    @patch("reset_notifications.urlopen")
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

        self.assertListEqual(
            [{"namespace": "sample", "name": "hello-image"}], next(fetcher)
        )
        self.assertListEqual(
            [{"namespace": "sample", "name": "another-image"}], next(fetcher)
        )
        with self.assertRaises(StopIteration):
            next(fetcher)


if __name__ == "__main__":
    unittest.main()
