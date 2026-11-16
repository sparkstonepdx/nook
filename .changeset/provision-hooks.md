---
"nook": minor
---

Services get versions and provisioning (#42 step 1). A provider states `"version"`, and consumers can need `"postgres>=15"` or `"php@8.3"`: binding refuses a provider that doesn't meet it and auto-bind picks the newest that does. A provider's `"provision"` hook runs when an app is bound and prints the app's env (its own login and database); `"deprovision"` runs when the binding goes.
