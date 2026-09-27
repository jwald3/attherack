# Security Policy

## The threat model, up front

At The Rack is designed to run on your own machine or a trusted private
network. **It has no authentication.** Anyone who can reach the listening port
can read all of your data and use your saved Anthropic API key. This is by
design for local, single-user, self-hosted use — it is not a bug.

By default the app listens on `127.0.0.1:8080`, which only accepts connections
from the same computer. If you expose it more widely (`ADDR=:8080`, a Docker
port mapping to `0.0.0.0`, a reverse proxy, etc.), you are responsible for
putting authentication in front of it — a VPN such as Tailscale, or an
authenticating reverse proxy. See the "Security" note in the
[README](README.md#configuration).

Your Anthropic API key and all training data stay in a local SQLite file. The
only data that leaves your machine is what's sent to the Anthropic API during a
chat (your messages, any attached photos, and whatever the coach's tools read
to answer them).

## Reporting a vulnerability

If you find a vulnerability that is *not* covered by the no-authentication
design above — for example, a way for the app to leak data or execute code
beyond what the threat model expects — please report it privately rather than
opening a public issue.

Use GitHub's [private vulnerability
reporting](https://github.com/jwald3/attherack/security/advisories/new)
("Report a vulnerability" under the repository's **Security** tab). Please
include steps to reproduce and the impact you observed.

You can expect an initial response within a week. Thanks for helping keep the
project safe.
