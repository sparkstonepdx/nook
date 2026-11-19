---
"nook": minor
---

Move an app's service to another provider with its data: `nook-admin migrate APP SERVICE TO`, or **Move** beside a binding on the Services page. The old provider's `export` hook streams into the new one's `import` hook while the app is stopped; on failure the app goes back to its old binding unchanged. With the catalog's Postgres hooks, this moves a database from postgres-16 to postgres-17 (#42 step 4).
