"""Python client for the gocrapling scraping server."""

from .client import (
    Captured,
    Client,
    CrawlResult,
    FetchResult,
    GocraplingError,
)

__all__ = ["Client", "FetchResult", "CrawlResult", "Captured", "GocraplingError"]
__version__ = "0.1.0"  # x-release-please-version
