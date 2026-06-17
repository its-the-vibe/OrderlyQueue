# OrderlyQueue
![CI](https://github.com/your-org/OrderlyQueue/actions/workflows/ci.yaml/badge.svg)

A Go-based service that serializes Pull Request merges by coordinating with Redis, waiting for successful CI/CD deployment completion, and enforcing a configurable delay before releasing the next PR for merge.

## Architecture
OrderlyQueue acts as an orchestrator between GitHub (via Redis events), Poppit (merge execution), and CI/CD systems.

1. **Lock Check**: Ensures only one PR is processed at a time.
2. **Fetch PR**: Blocks until a PR URL is available in the Redis queue.
3. **Dispatch Merge**: Sends a merge command to Poppit.
4. **Acquire Lock**: Creates a Redis lock with a configurable expiry.
5. **Wait for Merge**: Listens for a GitHub "closed & merged" event.
6. **Wait for CI/CD**: Listens for a CI/CD completion event matching the merge SHA.
7. **Delay**: Updates lock expiry to a delay duration before allowing the next merge.

## Setup
1. Copy `config/config.example.yaml` to `config/config.yaml` and adjust settings.
2. Copy `.env.example` to `.env` and set `REDIS_PASSWORD`.
3. Build the service: `make build`
4. Run with Docker: `docker-compose up -d`

## Configuration
- `config/config.yaml`: Main configuration for Redis keys, channels, and timeouts.
- `.env`: Sensitive credentials (Redis password).

## Redis Channels/Keys
- `orderlyq:current-task`: Lock key containing the PR URL.
- `orderlyq:pr-queue`: List where PR URLs are queued.
- `poppit:tasks`: List where merge commands are dispatched.
- `github:events`: Channel for GitHub PR events.
- `cicd:events`: Channel for CI/CD completion events.
