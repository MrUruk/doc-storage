"""S3 object storage for raw uploads and rendered DOCX artifacts.

boto3 is synchronous, so blocking calls are dispatched to a thread to keep the
async handlers responsive. The same code targets real AWS S3 (no endpoint_url)
or a local MinIO (S3_ENDPOINT_URL set) — see config.S3Config.
"""

from __future__ import annotations

import asyncio

import boto3
from botocore.client import Config as BotoConfig
from botocore.exceptions import ClientError

from .config import S3Config


class ObjectStore:
    def __init__(self, config: S3Config) -> None:
        self._config = config
        self._client = boto3.client(
            "s3",
            region_name=config.region,
            endpoint_url=config.endpoint_url,
            aws_access_key_id=config.access_key_id,
            aws_secret_access_key=config.secret_access_key,
            config=BotoConfig(
                s3={"addressing_style": "path" if config.force_path_style else "auto"},
                signature_version="s3v4",
            ),
        )

    @property
    def bucket(self) -> str:
        return self._config.bucket

    async def ensure_bucket(self) -> None:
        await asyncio.to_thread(self._ensure_bucket_sync)

    def _ensure_bucket_sync(self) -> None:
        try:
            self._client.head_bucket(Bucket=self._config.bucket)
            return
        except ClientError:
            pass
        try:
            self._client.create_bucket(Bucket=self._config.bucket)
        except ClientError:
            # Bucket may already exist / be owned elsewhere; surface on first put.
            pass

    async def put(
        self, key: str, body: bytes, content_type: str = "application/octet-stream"
    ) -> str:
        """Upload bytes and return the object key."""
        await asyncio.to_thread(self._put_sync, key, body, content_type)
        return key

    def _put_sync(self, key: str, body: bytes, content_type: str) -> None:
        self._client.put_object(
            Bucket=self._config.bucket,
            Key=key,
            Body=body,
            ContentType=content_type,
        )

    async def presigned_url(self, key: str, expires_in: int = 3600) -> str:
        return await asyncio.to_thread(self._presign_sync, key, expires_in)

    def _presign_sync(self, key: str, expires_in: int) -> str:
        return self._client.generate_presigned_url(
            "get_object",
            Params={"Bucket": self._config.bucket, "Key": key},
            ExpiresIn=expires_in,
        )
