---
name: jelly-cms
description: Use the Jelly CMS API v1 with scoped access keys, discoverable endpoints, and safe versioned writes.
---

# Jelly CMS API v1

## Authentication and discovery

Send the access key only in the `Authorization: Bearer jly_…` header. Do not put keys in source code, URLs, command history, or logs.

Start with `GET /api/v1` and `GET /api/v1/openapi.json` to discover the available endpoints. Then call `GET /api/v1/me` to confirm the key and its effective permissions.

## Content workflow

Read posts with `GET /api/v1/posts` or `GET /api/v1/posts/{id}`. Create a draft with `POST /api/v1/posts` and a body whose status is `draft`.

List endpoints return `{ "items", "total", "page", "per_page" }`. `GET /api/v1/posts` and `GET /api/v1/pages` return summaries in `items`: they have no `content` or `source`. To read the full body, call `GET /api/v1/posts/{id}`. `page` defaults to 1 (maximum 1000000) and `per_page` defaults to 20 (maximum 100). A value that is not an integer, or is out of range, returns `400 bad_request`. `status` filters by exact match and `q` searches titles. `/products`, `/orders`, and `/media/files` use the same `page` and `per_page` rules.

Before updating a post, page, or product, read its `updated_at` value. Include that exact value as `base_updated_at` in the update request. A `409 version_conflict` means someone changed the resource first: fetch it again, merge the intended changes, and retry using the new `updated_at`.

## Errors and activity

Errors use `{ "code", "message", "details", "correlation_id" }`. The `correlation_id` matches the response's `X-Request-ID`; include it when reporting a failure.

With `logs.read`, call `GET /api/v1/me/events` to inspect this integration's own request history. It is cursor-paged: pass `limit`, and pass the returned `next_cursor` as `cursor` to get the next page.

Each integration has a per-minute request limit shared by all of its keys. When a limit is exceeded, the API returns `429 rate_limited` with a `Retry-After` header in seconds. Wait that long, then retry. Do not retry in a tight loop.

Keep the access key out of request bodies and diagnostic output.
