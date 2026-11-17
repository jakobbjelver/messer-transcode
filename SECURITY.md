# Security

## Reporting

Open a **private security advisory** on GitHub (Security → Report a vulnerability). Do not open a public
issue for a vulnerability. This is a single-person project, not a company: expect an answer within a few
days, not within an hour.

## What this software assumes

- **It is not multi-tenant.** One owner, one library, one machine. There are no accounts and no roles;
  the API is protected by a bearer token and by whatever sits in front of it.
- **The container cannot reach the media.** The orchestrator holds the queue and the decisions; only the
  native node mounts the library. That separation is a safety property, not a deployment detail — do not
  "fix" it by sharing the volume with the container.
- **Nothing is deleted without a verified replacement.** An encode lands in scratch, is verified, is
  `fsync`ed and atomically renamed; the original moves to a dated quarantine. A path that deletes first
  is a vulnerability in this project's terms, whatever the CVE database says.
- **The config file is trusted.** It names paths that the node will write to. Run the node as a user that
  owns those paths, and never point `scratch` at a directory someone else can write.
- **The dashboard is read-mostly.** Mutating endpoints require the token; the read API is meant to sit
  behind your own access layer, not on the open internet.
