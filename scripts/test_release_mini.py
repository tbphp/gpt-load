import io
import json
import unittest
from unittest.mock import patch

from scripts.release_mini import ReleaseMiniError, _run_real_request


class MiniAccessKeyTest(unittest.TestCase):
    def test_real_request_creates_its_own_key_without_existing_keys(self):
        def request(base_url, method, path, auth_key, body=None):
            if (method, path) == ("POST", "/api/access-keys"):
                self.assertEqual(body, {"name": "release-acceptance"})
                return {"code": 0, "data": {"id": 42, "key": "test-access-key"}}
            self.assertEqual(method, "GET")
            self.assertTrue(path.startswith("/api/logs?"))
            self.assertIn("access_key_id=42", path)
            return {"code": 0, "data": {"items": [
                {"client_model": "gpt-6-luna", "status": "success"}
            ]}}

        with (
            patch("scripts.release_mini._request", side_effect=request),
            patch("scripts.release_mini.urlopen", return_value=io.BytesIO(
                json.dumps({"id": "resp-test"}).encode()
            )) as upstream,
        ):
            _run_real_request("http://127.0.0.1:3001", "test-auth")

        sent = upstream.call_args.args[0]
        self.assertEqual(sent.get_header("Authorization"), "Bearer test-access-key")
        self.assertEqual(sent.full_url, "http://127.0.0.1:3001/v1/responses")

    def test_invalid_created_key_stops_before_upstream_request(self):
        for data in ({}, {"id": 42}, {"id": 42, "key": ""}, {"key": "test-key"}):
            with (
                self.subTest(data=data),
                patch("scripts.release_mini._request", return_value={"data": data}),
                patch("scripts.release_mini.urlopen") as upstream,
            ):
                with self.assertRaises(ReleaseMiniError):
                    _run_real_request("http://127.0.0.1:3001", "test-auth")
                upstream.assert_not_called()


if __name__ == "__main__":
    unittest.main()
