---
name: jelly-cms
description: Use the Jelly CMS agent API with scoped keys, discoverable endpoints, and safe versioned writes.
---

# Jelly CMS Agent API

## Authentication and discovery

Send the agent key only in the `Authorization: Bearer jla_…` header. Do not put keys in source code, URLs, command history, or logs.

Start with `GET /api/agent/v1` and `GET /api/agent/v1/openapi.json` to discover the available endpoints. Then call `GET /api/agent/v1/me` to confirm the key and its effective permissions.

## Content workflow

Read posts with `GET /api/agent/v1/posts` or `GET /api/agent/v1/posts/{id}`. Create a draft with `POST /api/agent/v1/posts` and a body whose status is `draft`.

Before updating a post, page, or product, read its `updated_at` value. Include that exact value as `base_updated_at` in the update request. A `409 version_conflict` means someone changed the resource first: fetch it again, merge the intended changes, and retry using the new `updated_at`.

## Errors and activity

Errors use `{ "code", "message", "details", "correlation_id" }`. The `correlation_id` matches the response's `X-Request-ID`; include it when reporting a failure.

With `logs.read`, call `GET /api/agent/v1/logs` to inspect this agent's own request history. Keep the key out of request bodies and diagnostic output.
