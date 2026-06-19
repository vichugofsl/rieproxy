# rieproxy

**A tiny, zero-dependency local HTTP front end for the AWS Lambda Runtime Interface Emulator (RIE).**

`rieproxy` is a single static Go binary that turns each incoming HTTP request
into an API Gateway event, POSTs it to a Lambda RIE, and writes the Lambda
response back to the client. It lets you `curl` or browse a Lambda function
running locally — **without Node.js, Python, Docker-for-the-proxy, or the
Serverless Framework.**

```
  HTTP client ──▶ rieproxy ──▶ Lambda RIE (/2015-03-31/functions/{fn}/invocations) ──▶ your handler
              ◀──          ◀──
```

The RIE only speaks raw Lambda invoke JSON — it does **not** translate HTTP into
API Gateway events ([aws/aws-lambda-runtime-interface-emulator#64](https://github.com/aws/aws-lambda-runtime-interface-emulator/issues/64)).
`rieproxy` fills exactly that gap.

## Why

- **One static binary, one dependency.** The only module dependency is
  [`aws-lambda-go`](https://github.com/aws/aws-lambda-go); everything else is the
  Go standard library. Nothing to `npm install`, no Python, no Docker image to
  pull for the proxy. Drops cleanly into dev containers and CI.
- **Both API Gateway payload formats.** `--payload 1.0` (REST / proxy events)
  and `--payload 2.0` (HTTP API / Function URL events). Most alternatives do one
  or the other.
- **Dev-friendly defaults.** Permissive CORS, binary-body (base64) handling,
  optional RIE crash-recovery, and optional colorized container log tailing.

## Install

```sh
go install github.com/vichugofsl/rieproxy/cmd/rieproxy@latest
```

Or grab a prebuilt binary from the [Releases](https://github.com/vichugofsl/rieproxy/releases) page.

## Quickstart

1. Run your Lambda under the RIE (here, a container image that bundles the
   emulator). The RIE listens on `:8080` inside the container:

   ```sh
   docker run --rm -p 9000:8080 my-lambda-image
   ```

2. Point `rieproxy` at it:

   ```sh
   rieproxy --target 127.0.0.1:9000 --payload 2.0
   ```

3. Call your function like a normal HTTP server:

   ```sh
   curl http://127.0.0.1:3000/api/hello
   ```

## Usage

```
rieproxy [flags]

  --port                local HTTP server port                    (default 3000,  env RIEPROXY_PORT)
  --target              Lambda RIE endpoint (host:port or URL)     (default http://127.0.0.1:8080, env RIEPROXY_TARGET)
  --function            function name in the RIE invoke path       (default "function", env RIEPROXY_FUNCTION)
  --payload             API Gateway payload version: 1.0 or 2.0    (default 1.0,   env RIEPROXY_PAYLOAD)
  --timeout             per-invocation timeout                     (default 5m,    env RIEPROXY_TIMEOUT)
  --cors                send permissive CORS headers               (default true)
  --no-color            disable ANSI colors
  --restart-container   docker container to restart on failure     (optional, env RIEPROXY_RESTART_CONTAINER)
  --logs                docker container to tail logs from         (optional, repeatable)
  --version             print version and exit
```

Flags take precedence over environment variables, which take precedence over
defaults.

## Scope (what it is and isn't)

`rieproxy` forwards **all** traffic to a **single** Lambda function. It does
**not** parse `serverless.yml`, CloudFormation, CDK, Terraform, or any other
infrastructure-as-code, and it does not route between multiple functions. This
is intentional and matches how many teams run a single "API in a Lambda"
(e.g. a Gin/Echo/Chi/Fiber app via
[`aws-lambda-go-api-proxy`](https://github.com/awslabs/aws-lambda-go-api-proxy),
an Express/Fastify app via `serverless-http`, or a PHP app via Bref) where the
*application* does its own routing.

If you need IaC parsing and multi-function routing, use
[AWS SAM CLI](https://docs.aws.amazon.com/serverless-application-model/latest/developerguide/using-sam-cli-local-start-api.html)
or [serverless-offline](https://github.com/dherault/serverless-offline) instead.

## How it compares

| | rieproxy | bref/local-api-gateway | aws-lambda-rie-http | SAM `local start-api` | serverless-offline |
|---|---|---|---|---|---|
| Language / runtime | **Go (static binary)** | TypeScript / Node | Node | Python | Node |
| Install | `go install` / binary | Docker image | npm | pip + Docker | npm plugin |
| Extra runtime needed | **none** | Node or Docker | Node | Python + Docker | Node + Serverless Fw |
| Payload v1 / v2 | **both** | v2 | both | both | both |
| Parses IaC | no | no | no | **yes** (SAM/CFN) | **yes** (serverless.yml) |
| Multi-function routing | no | no | no | **yes** | **yes** |
| CORS / binary body | yes / yes | partial | — | yes | yes |

`rieproxy`'s niche is the leftmost column: a dependency-free Go binary that does
both payload formats. If you want the absolute minimum to put an HTTP face on a
single Lambda RIE in a dev container or CI job, that's this tool.

## Development

```sh
go test ./...        # unit tests (stdlib only)
go vet ./...
go build ./cmd/rieproxy
```

## License

[Apache-2.0](./LICENSE).

> "AWS", "AWS Lambda", and "Amazon API Gateway" are trademarks of Amazon.com,
> Inc. or its affiliates. This project is an independent tool that works with
> the AWS Lambda Runtime Interface Emulator and is not affiliated with or
> endorsed by AWS.
