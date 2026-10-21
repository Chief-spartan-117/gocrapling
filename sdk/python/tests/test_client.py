"""Run against a live server:  GOCRAPLING_URL=... GOCRAPLING_TOKEN=... GOCRAPLING_TARGET=... python -m unittest"""
import os
import unittest

from gocrapling import Client, GocraplingError

URL = os.environ.get("GOCRAPLING_URL")
TARGET = os.environ.get("GOCRAPLING_TARGET", "")


@unittest.skipUnless(URL and TARGET, "needs a running server (see module docstring)")
class LiveTests(unittest.TestCase):
    def setUp(self):
        self.c = Client(URL, token=os.environ.get("GOCRAPLING_TOKEN"))

    def test_fetch_css(self):
        self.assertEqual(self.c.fetch(TARGET, css="h1::text").results, ["Hello"])

    def test_fetch_markdown_strips_hidden(self):
        r = self.c.fetch(TARGET, ai_targeted=True)
        self.assertIn("# Hello", r.content)
        self.assertNotIn("IGNORE ME", r.content)

    def test_crawl(self):
        r = self.c.crawl(TARGET, {"title": "h1::text"}, max_pages=3)
        self.assertTrue(r.items)

    def test_error_mapping(self):
        with self.assertRaises(GocraplingError) as cm:
            self.c.fetch("file:///etc/hosts")
        self.assertEqual((cm.exception.status, cm.exception.code), (400, "invalid_url"))

    def test_bad_token(self):
        with self.assertRaises(GocraplingError) as cm:
            Client(URL, token="wrong").fetch(TARGET)
        self.assertEqual(cm.exception.status, 401)

    def test_openapi(self):
        self.assertIn("/v1/fetch", self.c.openapi()["paths"])

    def test_unreachable(self):
        with self.assertRaises(GocraplingError):
            Client("http://127.0.0.1:1", token="x", timeout=2).fetch(TARGET)


if __name__ == "__main__":
    unittest.main()
