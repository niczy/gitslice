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


class WorkspaceTests(unittest.TestCase):
    def test_write_and_read_round_trip(self):
        transport = FakeTransport(
            [
                (
                    200,
                    json.dumps(
                        {
                            "workspaceId": "demo",
                            "name": "demo",
                            "description": "",
                            "createdBy": "tester",
                            "owners": ["tester"],
                            "headCommitHash": "init-demo",
                            "createdAt": "1",
                            "updatedAt": "1",
                            "pathCount": 0,
                            "fileCount": 0,
                            "isRoot": False,
                        }
                    ).encode("utf-8"),
                ),
                (
                    200,
                    json.dumps(
                        {
                            "workspaceId": "demo",
                            "path": "README.md",
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
                            "workspaceId": "demo",
                            "path": "README.md",
                            "content": base64.b64encode(b"hello\n").decode("ascii"),
                            "size": "6",
                            "hash": "hash",
                        }
                    ).encode("utf-8"),
                ),
            ]
        )
        client = GitsliceClient(base_url="https://example.test", username="tester", transport=transport)
        workspace = client.workspace("demo")

        write_result = workspace.write("README.md", "hello\n")
        content = workspace.read("README.md")

        write_body = json.loads(transport.calls[1][3].decode("utf-8"))
        self.assertEqual(write_body["content"], base64.b64encode(b"hello\n").decode("ascii"))
        self.assertEqual(write_result.commit_hash, "commit-1")
        self.assertEqual(content, "hello\n")

    def test_write_many_and_search_parse(self):
        transport = FakeTransport(
            [
                (
                    200,
                    json.dumps(
                        {
                            "workspaceId": "demo",
                            "name": "demo",
                            "description": "",
                            "createdBy": "tester",
                            "owners": ["tester"],
                            "headCommitHash": "init-demo",
                            "createdAt": "1",
                            "updatedAt": "1",
                            "pathCount": 0,
                            "fileCount": 0,
                            "isRoot": False,
                        }
                    ).encode("utf-8"),
                ),
                (
                    200,
                    json.dumps(
                        {
                            "workspaceId": "demo",
                            "files": [
                                {"path": "a.py", "size": "3", "hash": "h1"},
                                {"path": "b.py", "size": "3", "hash": "h2"},
                            ],
                            "commitHash": "commit-batch",
                        }
                    ).encode("utf-8"),
                ),
                (
                    200,
                    json.dumps(
                        {
                            "workspaceId": "demo",
                            "query": "def main",
                            "glob": "**/*.py",
                            "matches": [
                                {
                                    "path": "a.py",
                                    "lineNumber": "2",
                                    "line": "def main():",
                                    "matchStart": "0",
                                    "matchEnd": "8",
                                }
                            ],
                        }
                    ).encode("utf-8"),
                ),
            ]
        )
        client = GitsliceClient(base_url="https://example.test", username="tester", transport=transport)
        workspace = client.workspace("demo")

        results = workspace.write_many({"a.py": "one", "b.py": "two"})
        matches = workspace.search("def main", glob="**/*.py")

        self.assertEqual(results[0].commit_hash, "commit-batch")
        self.assertEqual(matches[0].path, "a.py")
        self.assertEqual(matches[0].line_number, 2)

    def test_merge_accepts_workspace_instance(self):
        transport = FakeTransport(
            [
                (
                    200,
                    json.dumps(
                        {
                            "workspaceId": "main",
                            "name": "main",
                            "description": "",
                            "createdBy": "tester",
                            "owners": ["tester"],
                            "headCommitHash": "init-main",
                            "createdAt": "1",
                            "updatedAt": "1",
                            "pathCount": 0,
                            "fileCount": 0,
                            "isRoot": False,
                        }
                    ).encode("utf-8"),
                ),
                (
                    200,
                    json.dumps(
                        {
                            "workspaceId": "fork",
                            "name": "fork",
                            "description": "",
                            "createdBy": "tester",
                            "owners": ["tester"],
                            "headCommitHash": "init-fork",
                            "createdAt": "1",
                            "updatedAt": "1",
                            "pathCount": 0,
                            "fileCount": 0,
                            "isRoot": False,
                        }
                    ).encode("utf-8"),
                ),
                (
                    200,
                    json.dumps(
                        {
                            "workspaceId": "main",
                            "sourceWorkspaceId": "fork",
                            "baseWorkspaceId": "main",
                            "baseSnapshotId": "base-1",
                            "sourceSnapshotId": "source-1",
                            "targetSnapshotId": "target-1",
                            "status": "MERGE_STATUS_SUCCESS",
                            "commitHash": "commit-1",
                            "mergedPaths": ["README.md"],
                            "summary": {
                                "filesAdded": 0,
                                "filesModified": 1,
                                "filesDeleted": 0,
                                "linesAdded": 1,
                                "linesDeleted": 0,
                            },
                            "conflicts": [],
                        }
                    ).encode("utf-8"),
                ),
            ]
        )
        client = GitsliceClient(base_url="https://example.test", username="tester", transport=transport)
        main = client.workspace("main", cleanup_on_exit=False)
        fork = client.workspace("fork", cleanup_on_exit=False)

        result = main.merge(fork, message="merge fork")

        body = json.loads(transport.calls[2][3].decode("utf-8"))
        self.assertEqual(body["sourceWorkspaceId"], "fork")
        self.assertEqual(body["message"], "merge fork")
        self.assertEqual(result.base_snapshot_id, "base-1")
        self.assertEqual(result.merged_paths, ["README.md"])


if __name__ == "__main__":
    unittest.main()
