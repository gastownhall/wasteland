# Security Policy

## Reporting a Vulnerability

If you discover a security vulnerability in Wasteland, please report it responsibly:

1. **Do not** open a public issue for security vulnerabilities
2. Email the maintainers directly with details
3. Include steps to reproduce the vulnerability
4. Allow reasonable time for a fix before public disclosure

## Malicious "fix" attachments in issues, PRs, and discussions

Automated spam accounts have been posting on newly-opened issues across GitHub —
including in this project — with a friendly, issue-specific message and an
attached archive (for example a file named like `*_fix.zip`, `fix_win.zip`, or a
"patched build") that claims to solve your problem. **These files are malware. Do
not download or run them.**

How to stay safe:

- **Official builds and releases come only from this repository's
  [Releases](https://github.com/gastownhall/wasteland/releases) page** and the project's documented install
  instructions. Maintainers will never ask you to download a zip or executable
  posted in an issue, pull request, or discussion comment.
- A link that points to `github.com/user-attachments/files/...` is a file
  someone attached to a comment — it is **not** a vetted release asset, even
  though the URL is hosted on `github.com`.
- Be especially wary of a brand-new account offering a "fix" as a download
  within minutes of your post, or telling you to run an install command for a
  package or module that is not an official project source.

If you see one of these comments, please **report it** (the comment's `...` menu
→ *Report content*) and do not click the attachment. Note that deleting the
comment does not remove the uploaded file from GitHub's servers, so also report
the attachment to GitHub Support so it can be taken down.

## Scope

Wasteland is experimental software focused on DoltHub federation. Security considerations include:

- **Dolt operations**: Wasteland executes dolt CLI commands as the running user
- **DoltHub federation**: Wasteland communicates with DoltHub APIs using user tokens
- **Shell execution**: SQL scripts are executed via dolt subprocess calls
- **Config data**: Configuration and federation state stored in XDG directories

## Best Practices

When using Wasteland:

- Keep DOLTHUB_TOKEN secure and never commit it to version control
- Review wanted board content before acting on it
- Use appropriate DoltHub permissions for your organization
- Monitor federation activity via `wl browse` and `wl sync`

## Supported Versions

| Version | Supported          |
| ------- | ------------------ |
| 0.1.x   | :white_check_mark: |

## Updates

Security updates will be released as patch versions when applicable.
