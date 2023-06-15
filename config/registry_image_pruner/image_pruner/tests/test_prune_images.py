import logging
from flexmock import flexmock

from prune_images import remove_images


def test_remove_images(caplog):
    images = {
        "sha256-502c8c35e31459e8774f88e115d50d2ad33ba0e9dfd80429bc70ed4c1fd9e0cd.att": {
            "name": "sha256-502c8c35e31459e8774f88e115d50d2ad33ba0e9dfd80429bc70ed4c1fd9e0cd.att",
            "manifest_digest": "sha256:125c1d18ee1c3b9bde0c7810fcb0d4ffbc67e9b0c5b88bb8df9ca039bc1c9457",
        },
        "sha256-502c8c35e31459e8774f88e115d50d2ad33ba0e9dfd80429bc70ed4c1fd9e0cd.sbom": {
            "name": "sha256-502c8c35e31459e8774f88e115d50d2ad33ba0e9dfd80429bc70ed4c1fd9e0cd.sbom",
            "manifest_digest": "sha256:351326f899759a9a7ae3ca3c1cbdadcc8012f43231c145534820a68bdf36d55b",
        },
    }

    session_mock = flexmock()
    response_mock = flexmock()
    response_mock.should_receive("raise_for_status").and_return(None)
    session_mock.should_receive("delete").with_args(
        "https://quay.io/api/v1/repository/some/repository/tag/sha256-502c8c35e31459e8774f88e115d50d2ad33ba0e9dfd80429bc70ed4c1fd9e0cd.att"
    ).and_return(response_mock)
    session_mock.should_receive("delete").with_args(
        "https://quay.io/api/v1/repository/some/repository/tag/sha256-502c8c35e31459e8774f88e115d50d2ad33ba0e9dfd80429bc70ed4c1fd9e0cd.sbom"
    ).and_return(response_mock)

    with caplog.at_level(logging.INFO):
        remove_images(images, session_mock, "some/repository", dry_run=False)
    assert (
        "Removing image sha256-502c8c35e31459e8774f88e115d50d2ad33ba0e9dfd80429bc70ed4c1fd9e0cd.att from some/repository"
        in caplog.text
    )
    assert (
        "Removing image sha256-502c8c35e31459e8774f88e115d50d2ad33ba0e9dfd80429bc70ed4c1fd9e0cd.sbom from some/repository"
        in caplog.text
    )


def test_remove_images_dry_run(caplog):
    images = {
        "sha256-502c8c35e31459e8774f88e115d50d2ad33ba0e9dfd80429bc70ed4c1fd9e0cd.att": {
            "name": "sha256-502c8c35e31459e8774f88e115d50d2ad33ba0e9dfd80429bc70ed4c1fd9e0cd.att",
            "manifest_digest": "sha256:125c1d18ee1c3b9bde0c7810fcb0d4ffbc67e9b0c5b88bb8df9ca039bc1c9457",
        },
        "sha256-502c8c35e31459e8774f88e115d50d2ad33ba0e9dfd80429bc70ed4c1fd9e0cd.sbom": {
            "name": "sha256-502c8c35e31459e8774f88e115d50d2ad33ba0e9dfd80429bc70ed4c1fd9e0cd.sbom",
            "manifest_digest": "sha256:351326f899759a9a7ae3ca3c1cbdadcc8012f43231c145534820a68bdf36d55b",
        },
    }

    session_mock = flexmock()
    session_mock.should_receive("delete").never()
    session_mock.should_receive("delete").never()

    with caplog.at_level(logging.INFO):
        remove_images(images, session_mock, "some/repository", dry_run=True)
    assert (
        "Image sha256-502c8c35e31459e8774f88e115d50d2ad33ba0e9dfd80429bc70ed4c1fd9e0cd.att from some/repository should be removed"
        in caplog.text
    )
    assert (
        "Image sha256-502c8c35e31459e8774f88e115d50d2ad33ba0e9dfd80429bc70ed4c1fd9e0cd.sbom from some/repository should be removed"
        in caplog.text
    )


def test_remove_images_nothing_to_remove(caplog):
    images = {
        "sha256-502c8c35e31459e8774f88e115d50d2ad33ba0e9dfd80429bc70ed4c1fd9e0cd.att": {
            "name": "sha256-502c8c35e31459e8774f88e115d50d2ad33ba0e9dfd80429bc70ed4c1fd9e0cd.att",
            "manifest_digest": "sha256:125c1d18ee1c3b9bde0c7810fcb0d4ffbc67e9b0c5b88bb8df9ca039bc1c9457",
        },
        "sha256-502c8c35e31459e8774f88e115d50d2ad33ba0e9dfd80429bc70ed4c1fd9e0cd.sbom": {
            "name": "sha256-502c8c35e31459e8774f88e115d50d2ad33ba0e9dfd80429bc70ed4c1fd9e0cd.sbom",
            "manifest_digest": "sha256:351326f899759a9a7ae3ca3c1cbdadcc8012f43231c145534820a68bdf36d55b",
        },
        "app-image": {
            "name": "app-image",
            "manifest_digest": "sha256:502c8c35e31459e8774f88e115d50d2ad33ba0e9dfd80429bc70ed4c1fd9e0cd",
        },
    }

    session_mock = flexmock()
    session_mock.should_receive("delete").never()
    session_mock.should_receive("delete").never()
    with caplog.at_level(logging.INFO):
        remove_images(images, session_mock, "some/repository", dry_run=False)
    assert not caplog.text


def test_remove_images_multiple_images(caplog):
    images = {
        "sha256-502c8c35e31459e8774f88e115d50d2ad33ba0e9dfd80429bc70ed4c1fd9e0cd.att": {
            "name": "sha256-502c8c35e31459e8774f88e115d50d2ad33ba0e9dfd80429bc70ed4c1fd9e0cd.att",
            "manifest_digest": "sha256:125c1d18ee1c3b9bde0c7810fcb0d4ffbc67e9b0c5b88bb8df9ca039bc1c9457",
        },
        "sha256-502c8c35e31459e8774f88e115d50d2ad33ba0e9dfd80429bc70ed4c1fd9e0cd.sbom": {
            "name": "sha256-502c8c35e31459e8774f88e115d50d2ad33ba0e9dfd80429bc70ed4c1fd9e0cd.sbom",
            "manifest_digest": "sha256:351326f899759a9a7ae3ca3c1cbdadcc8012f43231c145534820a68bdf36d55b",
        },
        "sha256-5c55025c0cfc402b2a42f9d35b14a92b1ba203407d2a81aad7ea3eae1a3737d4.att": {
            "name": "sha256-5c55025c0cfc402b2a42f9d35b14a92b1ba203407d2a81aad7ea3eae1a3737d4.att",
            "manifest_digest": "sha256:5126ed26d60fffab5f82783af65b5a8e69da0820b723eea82a0eb71b0743c191",
        },
        "sha256-5c55025c0cfc402b2a42f9d35b14a92b1ba203407d2a81aad7ea3eae1a3737d4.sbom": {
            "name": "sha256-5c55025c0cfc402b2a42f9d35b14a92b1ba203407d2a81aad7ea3eae1a3737d4.sbom",
            "manifest_digest": "sha256:9b1f70d94117c63ee73d53688a3e4d412c1ba58d86b8e45845cce9b8dab44113",
        },
    }

    session_mock = flexmock()
    response_mock = flexmock()
    response_mock.should_receive("raise_for_status").and_return(None)
    session_mock.should_receive("delete").with_args(
        "https://quay.io/api/v1/repository/some/repository/tag/sha256-502c8c35e31459e8774f88e115d50d2ad33ba0e9dfd80429bc70ed4c1fd9e0cd.att"
    ).and_return(response_mock)
    session_mock.should_receive("delete").with_args(
        "https://quay.io/api/v1/repository/some/repository/tag/sha256-502c8c35e31459e8774f88e115d50d2ad33ba0e9dfd80429bc70ed4c1fd9e0cd.sbom"
    ).and_return(response_mock)
    session_mock.should_receive("delete").with_args(
        "https://quay.io/api/v1/repository/some/repository/tag/sha256-5c55025c0cfc402b2a42f9d35b14a92b1ba203407d2a81aad7ea3eae1a3737d4.att"
    ).and_return(response_mock)
    session_mock.should_receive("delete").with_args(
        "https://quay.io/api/v1/repository/some/repository/tag/sha256-5c55025c0cfc402b2a42f9d35b14a92b1ba203407d2a81aad7ea3eae1a3737d4.sbom"
    ).and_return(response_mock)

    with caplog.at_level(logging.INFO):
        remove_images(images, session_mock, "some/repository", dry_run=False)
    assert (
        "Removing image sha256-502c8c35e31459e8774f88e115d50d2ad33ba0e9dfd80429bc70ed4c1fd9e0cd.att from some/repository"
        in caplog.text
    )
    assert (
        "Removing image sha256-502c8c35e31459e8774f88e115d50d2ad33ba0e9dfd80429bc70ed4c1fd9e0cd.sbom from some/repository"
        in caplog.text
    )
    assert (
        "Removing image sha256-5c55025c0cfc402b2a42f9d35b14a92b1ba203407d2a81aad7ea3eae1a3737d4.att from some/repository"
        in caplog.text
    )
    assert (
        "Removing image sha256-5c55025c0cfc402b2a42f9d35b14a92b1ba203407d2a81aad7ea3eae1a3737d4.sbom from some/repository"
        in caplog.text
    )
