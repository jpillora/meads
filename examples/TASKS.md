## 0001 Set up CI pipeline

* status: closed
* priority: 5

Configure GitHub Actions to run tests and linting on every push and PR.

## 0002 Add user authentication

* status: inprogress
* priority: 4

Implement session-based auth with bcrypt password hashing. Need login,
logout, and signup endpoints.

## 0003 Write API documentation

* status: open
* priority: 2
* depends-on: 0002

Document all REST endpoints in OpenAPI format. Auth endpoints need to
be finalized before we can document them.

## 0004 Fix session expiry bug

* status: open
* priority: 5

The server returns a 500 instead of a 401 when a session cookie is
expired. Need to catch the error in the auth middleware and return
a proper response.

## 0005 Add rate limiting

* status: open
* priority: 3
* assignee: alice

Apply per-IP rate limiting to the login endpoint to prevent brute-force
attempts. Use a sliding window counter with a 15-minute window.

## 0006 Migrate database to PostgreSQL

* status: open
* priority: 1
* component: backend

Currently using SQLite which won't scale. Plan the migration path and
write a data migration script.

## 0007 Add search functionality

* status: open
* priority: 2
* depends-on: 0006

Full-text search across projects and tasks. Blocked until we're on
PostgreSQL since we'll use its built-in FTS.
