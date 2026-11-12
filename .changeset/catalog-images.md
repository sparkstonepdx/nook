---
"nook": minor
---

Store apps have their own page: screenshots, details, what they ask for, and links to the original project's homepage and source code. Catalog entries can list an `icon` and `screenshots` (PNG or JPEG in the catalog repo, up to 6, 1 MB each); `catalog-build` pins their hashes and adds the project's source link, and nook checks each image before the dashboard shows it. Also fixes the install sheet suggesting the name of an app that's already installed.
