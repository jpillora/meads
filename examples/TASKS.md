# TASKS

a [meads](https://github.com/jpillora/meads) (`md`) managed task log

* created: 2026-01-01T00:00:00Z
* updated: 2026-02-01T00:00:00Z
* next-id: 8

## 1 Set up CI pipeline

* status: closed
* priority: 5

Configure GitHub Actions to run tests and linting on every push and PR.

## 2 Add user authentication

* status: inprogress
* priority: 4

Implement session-based auth with bcrypt password hashing. Need login,
logout, and signup endpoints.

## 3 Write API documentation

* status: open
* priority: 2
* depends-on: 2

Document all REST endpoints in OpenAPI format. Auth endpoints need to
be finalized before we can document them.

## 4 Fix session expiry bug

* status: open
* priority: 5

The server returns a 500 instead of a 401 when a session cookie is
expired. Need to catch the error in the auth middleware and return
a proper response.

## 5 Add rate limiting

* status: open
* priority: 3
* assignee: alice

Apply per-IP rate limiting to the login endpoint to prevent brute-force
attempts. Use a sliding window counter with a 15-minute window.

## 6 Migrate database to PostgreSQL

* status: open
* priority: 1
* component: backend

Currently using SQLite which won't scale. Plan the migration path and
write a data migration script.

## 7 Add search functionality

* status: open
* priority: 2
* depends-on: 6

Full-text search across projects and tasks. Blocked until we're on
PostgreSQL since we'll use its built-in FTS.
