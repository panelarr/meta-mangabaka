# Security policy

## Reporting a vulnerability

Please report vulnerabilities **privately** through GitHub: open this repository's **Security** tab and choose **Report a vulnerability** (GitHub private vulnerability reporting). Don't open a public issue.

Include what you found, how to reproduce it and what impact you expect. We'll acknowledge the report, keep you updated and credit you in the advisory unless you'd rather stay anonymous.

## Supported versions

meta-mangabaka is in early development. Only the latest commit on `main` gets fixes.

## Threat model in short

- **Downloads are untrusted until verified.** The dump is checked against its published SHA-1 before it is decompressed, and the SQLite file is opened read-only.
- **Standalone mode** listens on the network. Set an API key so only your meta instance can call it.
- API keys are never logged.
