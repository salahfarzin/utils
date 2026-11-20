# Unreleased

Changes merged into `dev` since [v1.8.0](v1.8.0.md). When tagging, rename this file to the
new version, set the heading and date, and start a fresh `unreleased.md`.

## Fixes

- `kafka`: a consumer no longer stops for good when the broker restarts. Its loop took any
  read error wrapping `context.Canceled` or `context.DeadlineExceeded` for a shutdown, and
  kafka-go returns those for its own dial and group-join timeouts, so a consumer that
  started while Kafka was coming up quit minutes later while the service stayed up and
  healthy (notification's OTP emails stopped). Only the loop's own context ends it now;
  every read error is retried. `RunConsumerLoopWithContext` runs it until a context is
  done; `NewConsumer`, `NewSecureConsumer` and `RunConsumerLoop` behave as before
  otherwise. Services pick it up by bumping `utils`; no code changes.
