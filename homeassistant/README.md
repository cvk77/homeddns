# Home Assistant Add-on: HomeDDNS

![Supports aarch64 Architecture][aarch64-shield]
![Supports amd64 Architecture][amd64-shield]
![Supports armhf Architecture][armhf-shield]
![Supports armv7 Architecture][armv7-shield]
![Supports i386 Architecture][i386-shield]

[aarch64-shield]: https://img.shields.io/badge/aarch64-yes-green.svg
[amd64-shield]: https://img.shields.io/badge/amd64-yes-green.svg
[armhf-shield]: https://img.shields.io/badge/armhf-yes-green.svg
[armv7-shield]: https://img.shields.io/badge/armv7-yes-green.svg
[i386-shield]: https://img.shields.io/badge/i386-yes-green.svg

## About

Self-hosted Dynamic DNS service with support for multiple DNS providers. Perfect for updating your home's public IP address automatically when it changes.

**Supported DNS Providers:**
- Netcup CCP API
- AWS Route53

## Features

- 🌐 **Multi-Provider Support**: Works with Netcup and AWS Route53
- 🔐 **Secure**: HTTP Basic Authentication
- 🎯 **Wildcard DNS**: Full support for wildcard DNS records (e.g., `*.example.com`)
- 📱 **Ingress Support**: Access via Home Assistant UI
- 🔄 **Auto-Updates**: Compatible with all standard DynDNS clients
- 🚀 **Lightweight**: Minimal resource usage (~15MB RAM)

## Installation

1. Add this repository to your Home Assistant add-on store
2. Click "Install"
3. Configure the add-on (see Configuration section below)
4. Start the add-on
5. Check the logs to verify it started correctly

## Configuration

### Environment Overrides

The add-on reads user settings from `/data/options.json` by default. If your Supervisor setup stores `options.json` elsewhere, set `ADDON_OPTIONS_PATH` before starting the container to point to the desired file.

When Home Assistant launches the add-on (or you run the Docker image without CLI args), the binary auto-starts in `server` mode by default—no extra command is needed. If you invoke the binary manually, you can still run the `update` command.

### Basic Configuration

```yaml
auth_username: "dyndns"
auth_password: "your-secure-password" # plain text, not a hash
dns_provider: "netcup_ccp"
domain: "example.com"
dns_ttl: 60
port: 8053
```

### Netcup Provider

For Netcup, you'll need:
1. Your Netcup customer number
2. API Key (generated in Netcup CCP)
3. API Password (generated in Netcup CCP)

```yaml
netcup_customer_number: "123456"
netcup_api_key: "your-api-key"
netcup_api_password: "your-api-password"
```

**How to get Netcup API credentials:**
1. Log in to Netcup Customer Control Panel (CCP)
2. Go to: Stammdaten → API
3. Generate API Key and API Password
4. Copy your Customer Number from the account overview

### AWS Route53 Provider

For AWS Route53:

```yaml
dns_provider: "aws_route53"
aws_access_key_id: "AKIAIOSFODNN7EXAMPLE"
aws_secret_access_key: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
aws_region: "us-east-1"
```

**Note**: For security, create an IAM user with minimal permissions:
- `route53:ListHostedZones`
- `route53:ListResourceRecordSets`
- `route53:ChangeResourceRecordSets`

### Configuration Options

| Option | Required | Default | Description |
|--------|----------|---------|-------------|
| `auth_username` | Yes | `dyndns` | Username for Basic Authentication |
| `auth_password` | Yes | - | Your password for Basic Authentication |
| `dns_provider` | Yes | `netcup_ccp` | DNS provider (`netcup_ccp` or `aws_route53`; `route53` is accepted as an alias) |
| `domain` | No | - | Zone for hostnames within it (e.g., `example.co.uk`). Without it, Netcup uses the last two labels and Route53 discovers the zone |
| `dns_ttl` | No | `60` | DNS record TTL in seconds (30-86400) |
| `port` | No | `8053` | HTTP server port |
| `netcup_customer_number` | For Netcup | - | Your Netcup customer number |
| `netcup_api_key` | For Netcup | - | Netcup API key |
| `netcup_api_password` | For Netcup | - | Netcup API password |
| `aws_access_key_id` | For Route53 | - | AWS access key ID |
| `aws_secret_access_key` | For Route53 | - | AWS secret access key |
| `aws_region` | For Route53 | - | AWS region for the SDK (Route53 itself is global, e.g. `us-east-1`) |

## Usage

### Using Ingress (Recommended)

With Ingress enabled, you can access HomeDDNS through Home Assistant:
1. Click "Open Web UI" in the add-on page
2. Use the credentials configured in the add-on settings

### Using Direct Port Access

If you prefer direct access, configure your DynDNS client to use:

**URL:** `http://homeassistant.local:8053/nic/update?hostname=home.example.com&myip=1.2.3.4`

**Authentication:** Basic Auth with your configured username/password

**Note:** If your router reaches Home Assistant over the local network, the request source is a private LAN address, which HomeDDNS rejects with `911`. In that case the router must send the public address via `myip` (or `myipv6`).

### Router Configuration Examples

#### UniFi Dream Machine

1. Settings → Internet → WAN → Dynamic DNS
2. Create New Dynamic DNS:
   - Service: `custom`
   - Hostname: `home.example.com`
   - Username: `dyndns`
   - Password: Your configured password
   - Server: `homeassistant.local:8053/nic/update?hostname=%h&myip=%i`
   - Use this only if `%i` is a public address. Behind CGNAT it is a `100.64.0.0/10` address and is rejected; see the IPv6 / CGNAT section of the main README.

#### Fritz!Box

1. Internet → Freigaben → DynDNS
2. Configure:
   - DynDNS-Anbieter: `Benutzerdefiniert`
   - Update-URL: `http://homeassistant.local:8053/nic/update?hostname=<domain>&myip=<ipaddr>`
   - Domainname: `home.example.com`
   - Benutzername: `dyndns`
   - Kennwort: Your configured password

### Wildcard DNS

To update wildcard DNS records:

```bash
curl -u "dyndns:password" \
  "http://homeassistant.local:8053/nic/update?hostname=*.example.com&myip=1.2.3.4"
```

### Automatic IP Detection

Omit the `myip` parameter to use the request source IP:

```bash
curl -u "dyndns:password" \
  "http://homeassistant.local:8053/nic/update?hostname=home.example.com"
```

## Response Codes

| Code | Description |
|------|-------------|
| `good` | DNS record updated successfully |
| `nochg` | IP address unchanged, no update needed |
| `notfqdn` | Invalid hostname format |
| `911` | Server error or invalid IP address |

## Support

For issues and feature requests, please visit:
https://github.com/markussiebert/homeddns/issues

## License

See the main repository for license information.
