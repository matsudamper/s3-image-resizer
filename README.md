# s3-image-resizer

S3 互換ストレージ上の画像を縮小して、同じストレージに WebP で書き戻す HTTP API。

- 呼び出し元が発行した OIDC の JWT を検証し、その JWT で STS `AssumeRoleWithWebIdentity` して S3 を操作する。長期クレデンシャルは持たない
- 入力はマジックバイトで判定する。対応形式は JPEG / PNG / GIF / WebP / HEIC / HEIF。アニメーションは 1 フレーム目を使う
- EXIF の向きを反映してから縮小し、sRGB に変換し、メタデータを削除する
- 出力は WebP のみ

## API

### `POST /v1/resize`

ヘッダ: `Authorization: Bearer <JWT>`

```json
{
  "source":      { "bucket": "images", "key": "img/2026-11/abc.heic" },
  "destination": { "bucket": "images", "key": "img/2026-11/abc.thumb.webp" },
  "resize":      { "shortSide": 384, "maxLongSide": 1536 },
  "output":      { "format": "webp", "quality": 80 }
}
```

縮小率は `min(shortSide / 短辺, maxLongSide / 長辺, 1)`。拡大はしない。`quality` は省略時 80。

| ステータス | `error` | 意味 |
| --- | --- | --- |
| 204 | - | 成功。`destination` に書き込み済み |
| 400 | `INVALID_REQUEST` | JSON 不正、バケット不許可、キー不正、パラメータ範囲外 |
| 401 | `INVALID_TOKEN` | JWT が無い、または検証失敗 |
| 404 | `SOURCE_NOT_FOUND` | 元オブジェクトが無い |
| 413 | `TOO_LARGE` | `MAX_SOURCE_BYTES` または `MAX_SOURCE_PIXELS` 超過 |
| 415 | `UNSUPPORTED_FORMAT` | 対応外の形式 |
| 422 | `DECODE_FAILED` | デコード・変換・エンコードの失敗 |
| 502 | `STORAGE_ERROR` | STS / S3 の失敗 |
| 503 | `BUSY` | `MAX_CONCURRENCY` の空き待ちでタイムアウト |

エラーの本文は `{"error": "...", "message": "..."}`。

### `GET /healthz`

204 を返す。

## 実行

`docker-compose.yaml` の `environment` を書き換えて起動する。

```sh
docker compose up -d
```

イメージは `ghcr.io/matsudamper/s3-image-resizer`。タグ `v*` の push、または Actions の `Docker Release` を手動実行すると公開される。

## 開発

libvips（libheif 付き）が必要。

```sh
go test ./...
```
