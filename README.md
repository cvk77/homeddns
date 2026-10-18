# HomeDDNS - Self-hosted Dynamic DNS for Your Home

A lightweight, self-hosted DynDNS (Dynamic DNS) service with pluggable DNS provider support. Currently supports **Netcup** and **AWS Route53**. Perfect for home labs, homeservers, and Home Assistant users. Deploy with Docker Compose, Kubernetes, or as a Home Assistant addon.

## Features

- **Multi-Provider Plugin System**: Pluggable DNS provider architecture
  - **Netcup CCP API**: Updates DNS records via Netcup's Customer Control Panel API
  - **AWS Route53**: Updates DNS records via AWS Route53 API
- **Session Management**: Automatic session handling (Netcup: 15-minute timeout with proactive refresh)
- **Multiple Formats**: Supports both standard DynDNS format and UniFi custom provider format
- **IPv4 and IPv6**: Sets A and/or AAAA records (`myip`, `myipv6`), rejects private, CGNAT, link-local and other non-public addresses
- **Wildcard DNS**: Full support for wildcard DNS records (e.g., `*.example.com`)
- **Basic Authentication**: HTTP Basic Auth with constant-time comparison and a limit on failed logins
- **Hostname Allowlist**: Restrict which hostnames clients may update (`ALLOWED_HOSTNAMES`)
- **Flexible Deployment**: Run with Docker Compose, Kubernetes, or Home Assistant addon
- **Health Checks**: Built-in health endpoint for Kubernetes probes
- **Minimal Attack Surface**: Uses a distroless base image; the example Kubernetes manifests run it as non-root
- **IAM Role Support**: AWS Route53 provider supports IRSA (IAM Roles for Service Accounts)

## Why Self-Host Dynamic DNS?

Most modern routers already support the DynDNS (inadyn) API, and homelabbers typically own one or more domains. Instead of relying on third-party DynDNS services, you can reuse an existing domain and keep full ownership of your update pipeline. Self-hosting brings:

- **Control**: You choose the auth rules, uptime strategy, and see every update request.
- **Privacy**: Your Dynamic DNS credentials and logs stay within your own network or trusted infrastructure.
- **Reliability**: No upstream DynDNS provider to throttle or sunset a free tier.
- **Integration**: Works with any standard router or device that speaks DynDNS/HTTP update protocols, so you can keep your existing-compatible clients.

## Third-Party References

Mentions of **Netcup**, **AWS**, and **Route53** are strictly descriptive: they explain which external services the project integrates with. This project is independently developed and is not affiliated with, endorsed by, or sponsored by those companies or their products.

## Architecture

```
┌─────────────┐
│   Router    │  (UniFi, Fritz!Box, etc.)
│  /inadyn    │
└──────┬──────┘
       │ HTTP GET with Basic Auth
       ▼
┌─────────────────────────────┐
│  HomeDDNS Service           │
│  (Docker/K8s/Home Assistant)│
│  ┌───────────────────────┐  │
│  │ HTTP Server           │  │
│  │ - Auth Middleware     │  │
│  │ - DynDNS Handler      │  │
│  └───────┬───────────────┘  │
│          ▼                  │
│  ┌───────────────────────┐  │
│  │ DNS Provider Client   │  │
│  │ (Netcup CCP / Route53)│  │
│  └───────┬───────────────┘  │
└──────────┼──────────────────┘
           │ HTTPS
           ▼
   ┌───────────────────┐
   │ Netcup CCP API or │
   │ AWS Route53 API   │
   └───────────────────┘
```

## Prerequisites

- **Deployment Platform** (choose one):
  - Docker / Docker Compose
  - Kubernetes cluster
  - Home Assistant (add-on definition in [`homeassistant/`](homeassistant/))
- **DNS Provider** (choose one):
  - **Netcup**: Account with API credentials, domain managed by Netcup
  - **Route53**: AWS account with Route53 hosted zone

## Quick Start

### Docker Compose (Recommended for Home Use)

1. **Create `docker-compose.yml`:**

```yaml
version: '3.8'
services:
  homeddns:
    image: ghcr.io/markussiebert/homeddns:latest
    container_name: homeddns
    restart: unless-stopped
    ports:
      - "8053:8053"
    environment:
      - AUTH_USERNAME=dyndns
      - AUTH_PASSWORD=your-secure-password
      - DNS_PROVIDER=netcup_ccp
      - DOMAIN=example.com          # optional, see Configuration
      - ALLOWED_HOSTNAMES=home.example.com
      - DNS_TTL=60
      # Netcup credentials
      - NETCUP_CUSTOMER_NUMBER=123456
      - NETCUP_API_KEY=your-api-key
      - NETCUP_API_PASSWORD=your-api-password
```

2. **Start the service:**

```bash
docker-compose up -d
```

## Deployment Options

### Option 1: Docker Compose (Above)

### Option 2: Kubernetes

Example manifests are in [`k8s/`](k8s/):

| File | Purpose |
| ---- | ------- |
| `k8s/secret.example.yaml` | Credentials (copy and fill in) |
| `k8s/deployment.yaml` | Deployment for the Netcup provider |
| `k8s/deployment-route53.yaml` | Deployment for Route53, including an IRSA ServiceAccount |
| `k8s/service.yaml` | ClusterIP service on port 80 |

The deployments use `/health` for readiness and liveness probes and run as UID 65532 with a read-only root filesystem.

### 1. Create Kubernetes Secret

Create a secret with your credentials:

```bash
kubectl create secret generic homeddns-secret \
  --from-literal=auth-username=dyndns \
  --from-literal=auth-password='your-secure-password' \
  --from-literal=netcup-customer-number=123456 \
  --from-literal=netcup-api-key=YOUR_API_KEY \
  --from-literal=netcup-api-password=YOUR_API_PASSWORD
```

**Netcup API Credentials:**

- Login to Netcup Customer Control Panel (CCP)
- Navigate to: Stammdaten → API
- Generate API Key and API Password
- Customer Number is your Netcup customer number

### 2. Deploy to Kubernetes

Adjust `ALLOWED_HOSTNAMES`, `DOMAIN` and `TRUSTED_PROXIES` in the deployment, then:

```bash
kubectl apply -f k8s/deployment.yaml   # or k8s/deployment-route53.yaml
kubectl apply -f k8s/service.yaml
```

### 3. Expose the Service

**Option A: Ingress (Recommended)**

Behind an Ingress, the controller is the direct peer. Set `TRUSTED_PROXIES` to the controller's pod network so the client address is taken from `X-Forwarded-For`; otherwise automatic IP detection sees the controller's private address and rejects the update.

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: homeddns
  annotations:
    cert-manager.io/cluster-issuer: letsencrypt-prod
spec:
  ingressClassName: nginx
  tls:
    - hosts:
        - dyndns.example.com
      secretName: dyndns-tls
  rules:
    - host: dyndns.example.com
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: homeddns
                port:
                  number: 80
```

**Option B: LoadBalancer**

```bash
kubectl patch service homeddns -p '{"spec":{"type":"LoadBalancer"}}'
```

## DNS Provider Configuration

This service uses a plugin system that supports multiple DNS providers. Choose the provider that matches your DNS hosting.

### Netcup Provider

The Netcup provider uses the CCP (Customer Control Panel) API to update DNS records.

**Environment Variables:**

- `DNS_PROVIDER=netcup_ccp` (default)
- `NETCUP_CUSTOMER_NUMBER` - Your Netcup customer number (required)
- `NETCUP_API_KEY` - API key from CCP (required)
- `NETCUP_API_PASSWORD` - API password from CCP (required)
- `DOMAIN` - Optional. The Netcup zone is taken from `DOMAIN` if the hostname is within it, otherwise from the last two labels of the hostname. Set it for multi-label TLDs such as `example.co.uk`.

Instead of the environment variables, the credentials can be stored in `~/.homeddns/netcup_credentials`.

**Example Deployment:**

```bash
kubectl apply -f k8s/deployment.yaml
```

**Features:**

- Automatic session management (15-min timeout, 10-min refresh)
- Support for all DNS record types (A, AAAA, CNAME, etc.)
- Wildcard DNS support

### AWS Route53 Provider

The Route53 provider uses the AWS SDK to update DNS records in Route53 hosted zones.

**Environment Variables:**

- `DNS_PROVIDER=aws_route53` (**required**: the default provider is `netcup_ccp`; `route53` is accepted as an alias)
- `ROUTE53_ZONE_ID` - Optional. Use this hosted zone for all updates and skip zone discovery
- `AWS_REGION` - Region for the AWS SDK (Route53 itself is global, e.g. `us-east-1`)
- AWS credentials via one of:
  - **IRSA (Recommended for EKS)**: IAM Role for Service Accounts
  - **IAM Instance Profile**: Attached to EC2 instances
  - **Environment Variables**: `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`
  - **Shared Credentials**: `~/.aws/credentials`

`DOMAIN` is not needed for Route53. Without `ROUTE53_ZONE_ID`, the hosted zone is discovered per hostname: the public hosted zone with the longest name that is a suffix of the hostname wins (so `home.dyn.example.com` uses a delegated `dyn.example.com` zone if it exists, and `home.example.co.uk` works). Private hosted zones are ignored.

**Example Deployment:**

```bash
kubectl apply -f k8s/deployment-route53.yaml
```

**Required IAM Permissions:**

With zone discovery:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": "route53:ListHostedZones",
      "Resource": "*"
    },
    {
      "Effect": "Allow",
      "Action": [
        "route53:ListResourceRecordSets",
        "route53:ChangeResourceRecordSets"
      ],
      "Resource": "arn:aws:route53:::hostedzone/*"
    }
  ]
}
```

Minimal, with a fixed `ROUTE53_ZONE_ID` (no `ListHostedZones` needed). Optionally restrict the record names with the `route53:ChangeResourceRecordSetsNormalizedRecordNames` condition key:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "route53:ListResourceRecordSets",
        "route53:ChangeResourceRecordSets"
      ],
      "Resource": "arn:aws:route53:::hostedzone/Z0123456789ABCDEFGHIJ"
    }
  ]
}
```

**IRSA Setup (EKS):**

1. Create IAM role with the above policy
2. Associate role with ServiceAccount:
   ```yaml
   apiVersion: v1
   kind: ServiceAccount
   metadata:
     name: homeddns-route53
     annotations:
       eks.amazonaws.com/role-arn: arn:aws:iam::ACCOUNT_ID:role/homeddns-route53
   ```
3. Deploy using `k8s/deployment-route53.yaml`

**Features:**

- Automatic hosted zone discovery (longest suffix match, public zones only) or a fixed zone via `ROUTE53_ZONE_ID`
- Hosted zone caching for performance
- Support for all DNS record types
- Wildcard DNS support
- Configurable TTL

### Adding Custom Providers

The plugin system makes it easy to add new DNS providers:

1. Implement the `provider.Provider` interface in `internal/provider/`
2. Register a factory in the file's `init()` function with `RegisterFactory("name", factory)`; the provider loads its own credentials
3. Update documentation

**Provider Interface:**

```go
type Provider interface {
    Name() string
    // GetRecord returns ErrRecordNotFound if the record does not exist.
    GetRecord(ctx context.Context, domain, hostname, recordType string) (*DNSRecord, error)
    // UpdateRecord reports changed=false if the record already had the value.
    UpdateRecord(ctx context.Context, domain string, record *DNSRecord) (changed bool, err error)
    // DeleteRecord deletes the record only if it still has the given value.
    DeleteRecord(ctx context.Context, domain string, record *DNSRecord) error
    Close(ctx context.Context) error
}
```

## Usage

### Standard DynDNS Format

```bash
curl -u "dyndns:your-password" \
  "https://dyndns.example.com/nic/update?hostname=home.example.com&myip=1.2.3.4"
```

Response: `good 1.2.3.4` or `nochg 1.2.3.4`

### IPv6 and Dual Stack

`myip` may be an IPv4 or IPv6 address. `myipv6` sets the AAAA record. If both are given, both records are set:

```bash
curl -u "dyndns:your-password" \
  "https://dyndns.example.com/nic/update?hostname=home.example.com&myip=1.2.3.4&myipv6=2001:db8::1"
```

Response: `good 1.2.3.4,2001:db8::1` (`nochg` if neither record changed)

IPv4-mapped addresses (`::ffff:1.2.3.4`) are treated as IPv4 and set as A record. Addresses with a zone ID (`fe80::1%eth0`) are rejected.

(`2001:db8::/32` is a documentation prefix and is rejected by the server; use your real address.)

### UniFi Custom Provider Format

```bash
curl -u "dyndns:your-password" \
  "https://dyndns.example.com/home.example.com"
```

Response: `good` or `nochg`

### Automatic IP Detection

Omit `myip`/`myipv6` to use the client address of the request:

```bash
curl -u "dyndns:your-password" \
  "https://dyndns.example.com/nic/update?hostname=home.example.com"
```

The client address is the TCP peer, unless the peer is listed in `TRUSTED_PROXIES`; then the `X-Forwarded-For` header is read from right to left and the first address that is not a trusted proxy is used. A client connecting over IPv6 sets the AAAA record, over IPv4 the A record.

### Rejected Addresses

Unless `ALLOW_PRIVATE_IPS=true`, the server answers `911` for addresses that cannot be a public DynDNS target: loopback, unspecified, multicast, link-local, RFC 1918 private, CGNAT (`100.64.0.0/10`), IPv6 ULA (`fc00::/7`) and documentation ranges (`192.0.2.0/24`, `198.51.100.0/24`, `203.0.113.0/24`, `2001:db8::/32`, `3fff::/20`). The reason is logged as a warning.

### Wildcard DNS

```bash
curl -u "dyndns:your-password" \
  "https://dyndns.example.com/nic/update?hostname=*.example.com&myip=1.2.3.4"
```

## IPv6 / CGNAT

Behind carrier-grade NAT the gateway has no public IPv4 address, but usually a public IPv6 address. HomeDDNS can then be updated over IPv6:

- **Leave out `myip`** in the router configuration. UniFi replaces `%i` with the WAN IPv4 address, which behind CGNAT is a `100.64.0.0/10` address and is rejected.
- Without `myip`, the server uses the source address of the connection. If the gateway connects over IPv6, that is the **WAN IPv6 address of the gateway**, not of a LAN host, and the AAAA record is set.
- The server must be **reachable via IPv6** (AAAA record for the server name), otherwise the gateway connects over IPv4. The server listens on `:<port>`, which in Go accepts IPv4 and IPv6 connections (dual stack). Behind a reverse proxy or Kubernetes Ingress, the proxy must be reachable via IPv6 and `TRUSTED_PROXIES` must be set.
- An old A record of the same name may still point to a wrong address. Set `CLEANUP_STALE_RECORD=true` to delete the A record when an AAAA update succeeds (and vice versa). Deletion only happens after a lookup of the existing record and only when a single address family is updated.
- For a LAN host (e.g. a server behind the gateway), the gateway's update does not help: the host has its own IPv6 address. Update a separate record for it with its own client, sending `myipv6`.

## Configuration

### Environment Variables

| Variable                 | Required          | Default      | Description |
| ------------------------ | ----------------- | ------------ | ----------- |
| `AUTH_USERNAME`          | Yes               | -            | Basic auth username |
| `AUTH_PASSWORD`          | Yes               | -            | Basic auth password (plain text, not a hash) |
| `DNS_PROVIDER`           | No                | `netcup_ccp` | `netcup_ccp` or `aws_route53` (alias `route53`) |
| `DOMAIN`                 | `update` command  | -            | Zone used for hostnames within it. Required for the `update` command, optional for the server |
| `ALLOWED_HOSTNAMES`      | No (recommended)  | all          | Comma-separated hostnames that may be updated; `*.suffix` matches any name below `suffix`. Other hostnames get `nohost` (HTTP 403) |
| `DNS_TTL`                | No                | `60`         | DNS record TTL in seconds |
| `TRUSTED_PROXIES`        | No                | none         | Comma-separated CIDRs or IPs whose `X-Forwarded-For` header is trusted |
| `ALLOW_PRIVATE_IPS`      | No                | `false`      | Accept private, CGNAT, link-local and other non-public addresses |
| `CLEANUP_STALE_RECORD`   | No                | `false`      | Delete the A record after an AAAA update and vice versa |
| `SSL`                    | No                | `false`      | Serve HTTPS (`true` or `1`) |
| `CERTFILE`               | With `SSL`        | `/ssl/fullchain.pem` | Certificate path; relative paths are resolved below `/ssl/` |
| `KEYFILE`                | With `SSL`        | `/ssl/privkey.pem`   | Key path; relative paths are resolved below `/ssl/` |
| `LOG_LEVEL`              | No                | `info`       | `debug`, `info`, `warn` or `error` |
| `PORT`                   | No                | `8053`       | Read and validated, but currently **not applied**: the listen port comes from the `--port` flag of the `server` command (default `8053`) |
| `ADDON_OPTIONS_PATH`     | No                | -            | Home Assistant `options.json`; its keys are exported as upper-case environment variables |
| `NETCUP_CUSTOMER_NUMBER` | Netcup            | -            | Netcup customer number |
| `NETCUP_API_KEY`         | Netcup            | -            | Netcup API key |
| `NETCUP_API_PASSWORD`    | Netcup            | -            | Netcup API password |
| `ROUTE53_ZONE_ID`        | No                | -            | Route53: fixed hosted zone, skips zone discovery |
| `AWS_REGION`, `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY` | Route53 | - | Standard AWS SDK configuration (or IRSA, instance profile, `~/.aws/credentials`) |

### Response Codes

| Response              | Description |
| --------------------- | ----------- |
| `good <ip>[,<ip6>]`   | DNS record(s) updated successfully |
| `nochg <ip>[,<ip6>]`  | IP address unchanged, no update needed |
| `notfqdn`             | Invalid hostname format |
| `nohost` (HTTP 403)   | Hostname not in `ALLOWED_HOSTNAMES` |
| `911`                 | Provider error, or invalid / non-public IP address |
| HTTP 401              | Missing or wrong credentials |
| HTTP 429              | Too many failed logins from this address (10 within 15 minutes) |

The UniFi path format (`/<hostname>`) returns only the status word, without addresses.

## Router Configuration

### UniFi Dream Machine / UDM-Pro

1. Navigate to: **Settings → Internet → WAN**
2. Scroll to **Dynamic DNS**
3. Click **Create New Dynamic DNS**
4. Configure:
   - **Service**: `custom`
   - **Hostname**: `home.example.com` (or `*.example.com` for wildcard)
   - **Username**: `dyndns` (or your configured username)
   - **Password**: Your DynDNS password
   - **Server**: `dyndns.example.com/nic/update?hostname=%h` (recommended, replace with your server name)

Without `myip`, the server uses the address the gateway connects from; this is the variant to use behind CGNAT (see [IPv6 / CGNAT](#ipv6--cgnat)). With a public WAN IPv4, `dyndns.example.com/nic/update?hostname=%h&myip=%i` also works.

**Path format `/%h`:** If you use the server value `dyndns.example.com/%h` instead, inadyn on the gateway may need `ddns-path = "/"` in `/etc/inadyn.conf`. This edit is made on the gateway itself and does not have to survive a firmware update, reprovisioning or a change in the UniFi UI:

```bash
# SSH into UniFi device
ssh admin@192.168.1.1

# Edit inadyn config
vi /etc/inadyn.conf

# Add this line in the custom provider section:
ddns-path = "/"

# Restart inadyn
killall inadyn
```

### Fritz!Box

1. Navigate to: **Internet → Freigaben → DynDNS**
2. Configure:
   - **DynDNS-Anbieter**: `Benutzerdefiniert`
   - **Update-URL**: `https://dyndns.example.com/nic/update?hostname=<domain>&myip=<ipaddr>&myipv6=<ip6addr>`
     (drop `&myipv6=<ip6addr>` for IPv4 only)
   - **Domainname**: `home.example.com`
   - **Benutzername**: `dyndns`
   - **Kennwort**: Your DynDNS password

## Development

### Prerequisites

- [mise](https://mise.jdx.dev/) - Development environment manager (recommended)
- Go 1.25.4 (managed by mise)
- [Ko](https://ko.build/) - Container image builder (optional)

### Quick Start with mise

All common tasks are available via mise:

```bash
# List all available tasks
mise tasks

# Build binary for current platform
mise run build

# Build for Linux (deployment)
mise run build:linux

# Build container image with Ko (no Dockerfile needed!)
mise run build:ko

# Run server locally
mise run run

# Run tests
mise run test

# Clean build artifacts
mise run clean
```

### Available mise Tasks

| Task | Description |
|------|-------------|
| `build` | Build homeddns binary for current platform |
| `build:linux` | Build for Linux AMD64 |
| `build:linux-arm` | Build for Linux ARM64 (Raspberry Pi) |
| `build:ko` | Build container image with Ko (local) |
| `build:ko-push` | Build and push container to registry |
| `build:ko-multiplatform` | Build multi-platform container image |
| `run` | Run homeddns server |
| `run:update` | Update DNS record (pass hostname as arg) |
| `test` | Run all tests |
| `test:provider` | Test provider package only |
| `fmt` | Format Go code |
| `clean` | Remove build artifacts |

### Local Testing

```bash
# Copy the example environment file
cp .env.local.example .env.local

# Edit .env.local with your credentials
# Then run with mise (automatically loads .env.local)
mise run run:dev

# Or export environment variables manually
export AUTH_USERNAME=dyndns
export AUTH_PASSWORD='your-password'
export DNS_PROVIDER=netcup_ccp
export DOMAIN=example.com
export NETCUP_CUSTOMER_NUMBER=123456
export NETCUP_API_KEY=your-api-key
export NETCUP_API_PASSWORD=your-api-password

# Run server
mise run run

# Test update command
mise run run:update -- test.example.com
```

### Test with curl

```bash
# Test health endpoint (no auth required)
curl http://localhost:8053/health

# Test DNS update
curl -u "dyndns:your-password" \
  "http://localhost:8053/nic/update?hostname=test.example.com&myip=1.2.3.4"
```

### Why Ko for Container Builds?

Ko builds optimized container images directly from Go code:
- ✅ **No Dockerfile needed** - builds directly from Go source
- ✅ **Smaller images** - optimized layers, distroless base (~15MB total)
- ✅ **SBOM included** - automatic software bill of materials
- ✅ **Multi-platform** - easy cross-compilation
- ✅ **Fast builds** - aggressive optimization flags built-in

### Binary Size Optimizations

Our build is optimized for minimal size:

| Build Type | Size | Optimizations |
|------------|------|---------------|
| Standard | ~19MB | Default Go build |
| **Optimized** | **~14MB** | Aggressive optimization flags (26% smaller) |

**Optimization flags explained:**
- `-trimpath` - Remove file system paths from binary
- `-s -w` - Strip debug info and symbol table
- `-extldflags=-static` - Create fully static binary (no external dependencies)
- `-tags netgo` - Use pure Go networking (no CGO required)

Result: **14MB static binary** that runs anywhere with no dependencies!

## Netcup API Reference

- **Endpoint**: `https://ccp.netcup.net/run/webservice/servers/endpoint.php?JSON`
- **Documentation**: https://www.netcup-wiki.de/wiki/CCP_API
- **Session Timeout**: 15 minutes
- **Supported Operations**:
  - `login` - Authenticate and obtain session ID
  - `logout` - Invalidate session
  - `infoDnsRecords` - Retrieve DNS records for a domain
  - `updateDnsRecords` - Update DNS records

## Security Considerations

- **TLS**: Basic Auth sends the password in every request. Use plain HTTP only behind a TLS-terminating reverse proxy or Ingress, or enable `SSL=true`.
- HTTP Basic Authentication for the DynDNS endpoint, compared in constant time; 10 failed logins from one address within 15 minutes lead to HTTP 429
- Set `ALLOWED_HOSTNAMES` so the credentials can only update the intended records
- `X-Forwarded-For` is ignored unless the peer is in `TRUSTED_PROXIES`
- The example Kubernetes manifests run the container as non-root (UID 65532) with a read-only root filesystem and without privilege escalation (the image itself defaults to root for Home Assistant compatibility)
- Minimal container image (distroless)
- All secrets stored in Kubernetes Secrets or environment variables

## Troubleshooting

### Check Logs

**Docker Compose:**
```bash
docker logs -f homeddns
```

**Kubernetes:**
```bash
kubectl logs -l app=homeddns --tail=100 -f
```

### Common Issues

**401 Unauthorized**

- Check that Basic Auth credentials are correct
- `AUTH_PASSWORD` is compared as plain text; do not configure a hash

**"notfqdn" Response**

- Hostname must be a valid FQDN

**"nohost" Response (HTTP 403)**

- The hostname is not listed in `ALLOWED_HOSTNAMES`

**"911" Response**

- The IP address is not public (private, CGNAT, link-local, ...); the log shows the rejected address. Send the public address via `myip`/`myipv6`, set `TRUSTED_PROXIES` when running behind a proxy, or set `ALLOW_PRIVATE_IPS=true` for internal use
- Check the provider credentials (Netcup API credentials, AWS credentials and IAM policy)
- Verify the domain / hosted zone exists in your provider account
- Check service logs for detailed error messages

**Session Timeout**

- Sessions are automatically refreshed every 10 minutes
- If you see frequent re-logins, check network connectivity to Netcup API

## License

This project inherits the license from the parent repository.

## Contributing

Contributions are welcome! Please ensure:

- Code follows Go best practices
- Tests are added for new features
- Documentation is updated
