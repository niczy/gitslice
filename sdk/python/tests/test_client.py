import json
import unittest

from gitslice.client import GitsliceClient
from gitslice.exceptions import NotFoundError, ValidationError


class FakeTransport:
    def __init__(self, responses):
        self.responses = list(responses)
        self.calls = []

    def __call__(self, method, url, headers, body):
        self.calls.append((method, url, headers, body))
        return self.responses.pop(0)


class ClientTests(unittest.TestCase):
    def test_workspace_auto_creates_on_not_found(self):
        transport = FakeTransport(
            [
                (404, json.dumps({"message": "workspace not found"}).encode("utf-8")),
                (
                    200,
                    json.dumps(
                        {
                            "workspaceId": "demo",
                            "name": "demo",
                            "description": "",
                            "createdBy": "tester",
                            "owners": ["tester"],
                            "headCommitHash": "cmt_init_demo",
                            "createdAt": "1",
                            "updatedAt": "1",
                            "pathCount": 0,
                            "fileCount": 0,
                            "isRoot": False,
                        }
                    ).encode("utf-8"),
                ),
            ]
        )
        client = GitsliceClient(base_url="https://example.test", username="tester", transport=transport)

        workspace = client.workspace("demo")

        self.assertEqual(workspace.workspace_id, "demo")
        self.assertEqual(len(transport.calls), 2)
        self.assertEqual(transport.calls[0][0], "GET")
        self.assertEqual(transport.calls[1][0], "POST")

    def test_workspace_auto_cleanup_defaults_to_created_workspaces(self):
        transport = FakeTransport(
            [
                (404, json.dumps({"message": "workspace not found"}).encode("utf-8")),
                (
                    200,
                    json.dumps(
                        {
                            "workspaceId": "demo",
                            "name": "demo",
                            "description": "",
                            "createdBy": "tester",
                            "owners": ["tester"],
                            "headCommitHash": "cmt_init_demo",
                            "createdAt": "1",
                            "updatedAt": "1",
                            "pathCount": 0,
                            "fileCount": 0,
                            "isRoot": False,
                        }
                    ).encode("utf-8"),
                ),
                (200, json.dumps({"workspaceId": "demo"}).encode("utf-8")),
            ]
        )
        client = GitsliceClient(base_url="https://example.test", username="tester", transport=transport)

        with client.workspace("demo") as workspace:
            self.assertEqual(workspace.workspace_id, "demo")

        self.assertEqual(transport.calls[2][0], "DELETE")

    def test_create_workspace_serializes_json(self):
        transport = FakeTransport(
            [
                (
                    200,
                    json.dumps(
                        {
                            "workspaceId": "demo",
                            "name": "Demo",
                            "description": "desc",
                            "createdBy": "tester",
                            "owners": ["tester"],
                            "headCommitHash": "cmt_init_demo",
                            "createdAt": "1",
                            "updatedAt": "2",
                            "pathCount": 0,
                            "fileCount": 0,
                            "isRoot": False,
                        }
                    ).encode("utf-8"),
                )
            ]
        )
        client = GitsliceClient(base_url="https://example.test", username="tester", transport=transport)

        info = client.create_workspace("demo", name="Demo", description="desc")

        body = json.loads(transport.calls[0][3].decode("utf-8"))
        self.assertEqual(body["workspaceId"], "demo")
        self.assertEqual(info.workspace_id, "demo")
        self.assertEqual(info.name, "Demo")

    def test_maps_validation_errors(self):
        transport = FakeTransport([(400, json.dumps({"message": "bad request"}).encode("utf-8"))])
        client = GitsliceClient(base_url="https://example.test", username="tester", transport=transport)

        with self.assertRaises(ValidationError):
            client.list_workspaces(limit=-1)

    def test_maps_not_found_errors(self):
        transport = FakeTransport([(404, json.dumps({"message": "missing"}).encode("utf-8"))])
        client = GitsliceClient(base_url="https://example.test", username="tester", transport=transport)

        with self.assertRaises(NotFoundError):
            client.get_workspace_info("missing")


if __name__ == "__main__":
    unittest.main()
