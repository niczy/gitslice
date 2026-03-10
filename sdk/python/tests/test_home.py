import base64
import json
import unittest

from gitslice.client import GitsliceClient


class FakeTransport:
    def __init__(self, responses):
        self.responses = list(responses)
        self.calls = []

    def __call__(self, method, url, headers, body):
        self.calls.append((method, url, headers, body))
        return self.responses.pop(0)


class HomeFilesystemTests(unittest.TestCase):
    def test_home_uses_absolute_paths_and_home_workspace(self):
        transport = FakeTransport(
            [
                (
                    200,
                    json.dumps(
                        {
                            "workspaceId": "home.tester",
                            "path": "/tester/README.md",
                            "size": "6",
                            "hash": "hash",
                            "commitHash": "commit-1",
                        }
                    ).encode("utf-8"),
                ),
                (
                    200,
                    json.dumps(
                        {
                            "workspaceId": "home.tester",
                            "path": "/tester/README.md",
                            "content": base64.b64encode(b"hello\n").decode("ascii"),
                            "size": "6",
                            "hash": "hash",
                        }
                    ).encode("utf-8"),
                ),
            ]
        )
        client = GitsliceClient(base_url="https://example.test", username="tester", transport=transport)
        home = client.home()

        write_result = home.write("/tester/README.md", "hello\n")
        content = home.read("/tester/README.md")

        self.assertEqual(write_result.workspace_id, "home.tester")
        self.assertEqual(content, "hello\n")

        write_body = json.loads(transport.calls[0][3].decode("utf-8"))
        self.assertEqual(write_body["workspaceId"], "home.tester")
        self.assertEqual(write_body["path"], "/tester/README.md")
        self.assertTrue(transport.calls[0][1].endswith("/v1/fs/workspaces/home.tester/files/%2Ftester%2FREADME.md"))
        self.assertTrue(transport.calls[1][1].endswith("/v1/fs/workspaces/home.tester/files/%2Ftester%2FREADME.md"))

    def test_home_resolves_username_from_current_user(self):
        transport = FakeTransport(
            [
                (
                    200,
                    json.dumps(
                        {
                            "id": "u1",
                            "username": "token-user",
                            "name": "Token User",
                        }
                    ).encode("utf-8"),
                ),
                (
                    200,
                    json.dumps({"entries": []}).encode("utf-8"),
                ),
            ]
        )
        client = GitsliceClient(base_url="https://example.test", api_key="token", transport=transport)

        home = client.home()
        entries = home.ls()

        self.assertEqual(entries, [])
        self.assertEqual(home.workspace_id, "home.token-user")
        self.assertEqual(transport.calls[0][1], "https://example.test/v1/users/me")
        self.assertTrue(transport.calls[1][1].endswith("/v1/fs/workspaces/home.token-user/ls/%2Ftoken-user"))

    def test_home_requires_absolute_paths(self):
        client = GitsliceClient(base_url="https://example.test", username="tester", transport=FakeTransport([]))
        home = client.home()

        with self.assertRaises(ValueError):
            home.write("README.md", "hello\n")


if __name__ == "__main__":
    unittest.main()
