# GPT-Load

[中文文档](README.md) | English

[![Release](https://img.shields.io/github/v/release/tbphp/gpt-load)](https://github.com/tbphp/gpt-load/releases)
![Go Version](https://img.shields.io/badge/Go-1.23+-blue.svg)
![License](https://img.shields.io/badge/license-MIT-green.svg)

A high-performance, enterprise-grade AI API transparent proxy service designed specifically for enterprises and developers who need to integrate multiple AI services. Built with Go, featuring intelligent key management, load balancing, and comprehensive monitoring capabilities, designed for high-concurrency production environments.

For detailed documentation, please visit [Official Documentation](https://www.gpt-load.com/docs)

## Features

- **Transparent Proxy**: Complete preservation of native API formats, supporting OpenAI and Google Gemini among other formats (continuously expanding)
- **Intelligent Key Management**: High-performance key pool with group-based management, automatic rotation, and failure recovery
- **Load Balancing**: Weighted load balancing across multiple upstream endpoints to enhance service availability
- **Smart Failure Handling**: Automatic key blacklist management and recovery mechanisms to ensure service continuity
- **Dynamic Configuration**: System settings and group configurations support hot-reload without requiring restarts
- **Enterprise Architecture**: Distributed leader-follower deployment supporting horizontal scaling and high availability
- **Modern Management**: Vue 3-based web management interface that is intuitive and user-friendly
- **Comprehensive Monitoring**: Real-time statistics, health checks, and detailed request logging
- **High-Performance Design**: Zero-copy streaming, connection pool reuse, and atomic operations
- **Production Ready**: Graceful shutdown, error recovery, and comprehensive security mechanisms

## Supported AI Services

GPT-Load serves as a transparent proxy service, completely preserving the native API formats of various AI service providers:

- **OpenAI Format**: Official OpenAI API, Azure OpenAI, and other OpenAI-compatible services
- **Google Gemini Format**: Native APIs for Gemini Pro, Gemini Pro Vision, and other models
- **Extensibility**: Plugin-based architecture design for rapid integration of new AI service providers and their native formats

## Quick Start

### System Requirements

- Go 1.23+ (for source builds)
- Docker (for containerized deployment)
- MySQL, PostgreSQL, or SQLite (for database storage)
- Redis (for caching and distributed coordination, optional)

### Method 1: Docker Quick Start

```bash
docker run -d --name gpt-load \
    -p 3001:3001 \
    -e AUTH_KEY=sk-123456 \
    -v "$(pwd)/data":/app/data \
    ghcr.io/tbphp/gpt-load:latest
```

> Login to the management interface with `sk-123456`: <http://localhost:3001>

### Method 2: Using Docker Compose (Recommended)

**Installation Commands:**

```bash
# Create Directory
mkdir -p gpt-load && cd gpt-load

# Download configuration files
wget https://raw.githubusercontent.com/tbphp/gpt-load/refs/heads/main/docker-compose.yml
wget -O .env https://raw.githubusercontent.com/tbphp/gpt-load/refs/heads/main/.env.example

# Start services
docker compose up -d
```

The default installation uses the SQLite version, which is suitable for lightweight, single-instance applications.

If you need to install MySQL, PostgreSQL, and Redis, please uncomment the required services in the `docker-compose.yml` file, configure the corresponding environment variables, and restart.

**Other Commands:**

```bash
# Check service status
docker compose ps

# View logs
docker compose logs -f

# Restart Service
docker compose down && docker compose up -d

# Update to latest version
docker compose pull && docker compose down && docker compose up -d
```

After deployment:

- Access Web Management Interface: <http://localhost:3001>
- API Proxy Address: <http://localhost:3001/proxy>

> Use the default authentication key `sk-123456` to login to the management interface. The authentication key can be modified via AUTH_KEY in the .env file.

### Method 3: Source Build

Source build requires a locally installed database (SQLite, MySQL, or PostgreSQL) and Redis (optional).

```bash
# Clone and build
git clone https://github.com/tbphp/gpt-load.git
cd gpt-load
go mod tidy

# Create configuration
cp .env.example .env

# Modify DATABASE_DSN and REDIS_DSN configurations in .env
# REDIS_DSN is optional; if not configured, memory storage will be enabled

# Run
make run
```

After deployment:

- Access Web Management Interface: <http://localhost:3001>
- API Proxy Address: <http://localhost:3001/proxy>

> Use the default authentication key `sk-123456` to login to the management interface. The authentication key can be modified via AUTH_KEY in the .env file.

### Method 4: Cluster Deployment

Cluster deployment requires all nodes to connect to the same MySQL (or PostgreSQL) and Redis, with Redis being mandatory. It's recommended to use unified distributed MySQL and Redis clusters.

**Deployment Requirements:**

- All nodes must configure identical `AUTH_KEY`, `DATABASE_DSN`, `REDIS_DSN`
- Leader-follower architecture where follower nodes must configure environment variable: `IS_SLAVE=true`

For details, please refer to [Cluster Deployment Documentation](https://www.gpt-load.com/docs/cluster)

## Configuration System

### Configuration Architecture Overview

GPT-Load adopts a dual-layer configuration architecture:

#### 1. Static Configuration (Environment Variables)

- **Characteristics**: Read at application startup, immutable during runtime, requires application restart to take effect
- **Purpose**: Infrastructure configuration such as database connections, server ports, authentication keys, etc.
- **Management**: Set via `.env` files or system environment variables

#### 2. Dynamic Configuration (Hot-Reload)

- **System Settings**: Stored in database, providing unified behavioral standards for the entire application
- **Group Configuration**: Behavior parameters customized for specific groups, can override system settings
- **Configuration Priority**: Group Configuration > System Settings
- **Characteristics**: Supports hot-reload, takes effect immediately after modification without application restart

### Static Configuration (Environment Variables)

#### Server Configuration

| Setting                   | Environment Variable               | Default         | Description                                     |
| ------------------------- | ---------------------------------- | --------------- | ----------------------------------------------- |
| Service Port              | `PORT`                             | 3001            | HTTP server listening port                      |
| Service Address           | `HOST`                             | 0.0.0.0         | HTTP server binding address                     |
| Read Timeout              | `SERVER_READ_TIMEOUT`              | 60              | HTTP server read timeout (seconds)              |
| Write Timeout             | `SERVER_WRITE_TIMEOUT`             | 600             | HTTP server write timeout (seconds)             |
| Idle Timeout              | `SERVER_IDLE_TIMEOUT`              | 120             | HTTP connection idle timeout (seconds)          |
| Graceful Shutdown Timeout | `SERVER_GRACEFUL_SHUTDOWN_TIMEOUT` | 10              | Service graceful shutdown wait time (seconds)   |
| Follower Mode             | `IS_SLAVE`                         | false           | Follower node identifier for cluster deployment |
| Timezone                  | `TZ`                               | `Asia/Shanghai` | Specify timezone                                |

#### Authentication & Database Configuration

| Setting             | Environment Variable | Default              | Description                                                                     |
| ------------------- | -------------------- | -------------------- | ------------------------------------------------------------------------------- |
| Authentication Key  | `AUTH_KEY`           | `sk-123456`          | Unique authentication key for accessing management interface and proxy requests |
| Database Connection | `DATABASE_DSN`       | `./data/gpt-load.db` | Database connection string (DSN) or file path                                   |
| Redis Connection    | `REDIS_DSN`          | -                    | Redis connection string, uses memory storage when empty                         |

#### Performance & CORS Configuration

| Setting                 | Environment Variable      | Default                       | Description                                     |
| ----------------------- | ------------------------- | ----------------------------- | ----------------------------------------------- |
| Max Concurrent Requests | `MAX_CONCURRENT_REQUESTS` | 100                           | Maximum concurrent requests allowed by system   |
| Enable CORS             | `ENABLE_CORS`             | true                          | Whether to enable Cross-Origin Resource Sharing |
| Allowed Origins         | `ALLOWED_ORIGINS`         | `*`                           | Allowed origins, comma-separated                |
| Allowed Methods         | `ALLOWED_METHODS`         | `GET,POST,PUT,DELETE,OPTIONS` | Allowed HTTP methods                            |
| Allowed Headers         | `ALLOWED_HEADERS`         | `*`                           | Allowed request headers, comma-separated        |
| Allow Credentials       | `ALLOW_CREDENTIALS`       | false                         | Whether to allow sending credentials            |

#### Logging Configuration

| Setting             | Environment Variable | Default               | Description                         |
| ------------------- | -------------------- | --------------------- | ----------------------------------- |
| Log Level           | `LOG_LEVEL`          | `info`                | Log level: debug, info, warn, error |
| Log Format          | `LOG_FORMAT`         | `text`                | Log format: text, json              |
| Enable File Logging | `LOG_ENABLE_FILE`    | false                 | Whether to enable file log output   |
| Log File Path       | `LOG_FILE_PATH`      | `./data/logs/app.log` | Log file storage path               |

### Dynamic Configuration (Hot-Reload)

Dynamic configuration is stored in the database and supports real-time modification through the web management interface, taking effect immediately without restart.

**Configuration Priority**: Group Configuration > System Settings

#### Basic Settings

| Setting            | Field Name                           | Default                 | Group Override | Description                                  |
| ------------------ | ------------------------------------ | ----------------------- | -------------- | -------------------------------------------- |
| Project URL        | `app_url`                            | `http://localhost:3001` | ❌             | Project base URL                             |
| Log Retention Days | `request_log_retention_days`         | 7                       | ❌             | Request log retention days, 0 for no cleanup |
| Log Write Interval | `request_log_write_interval_minutes` | 1                       | ❌             | Log write to database cycle (minutes)        |

#### Request Settings

| Setting                       | Field Name                | Default | Group Override | Description                                                                 |
| ----------------------------- | ------------------------- | ------- | -------------- | --------------------------------------------------------------------------- |
| Request Timeout               | `request_timeout`         | 600     | ✅             | Forward request complete lifecycle timeout (seconds)                        |
| Connection Timeout            | `connect_timeout`         | 15      | ✅             | Timeout for establishing connection with upstream service (seconds)         |
| Idle Connection Timeout       | `idle_conn_timeout`       | 120     | ✅             | HTTP client idle connection timeout (seconds)                               |
| Response Header Timeout       | `response_header_timeout` | 600     | ✅             | Timeout for waiting upstream response headers (seconds)                     |
| Max Idle Connections          | `max_idle_conns`          | 100     | ✅             | Connection pool maximum total idle connections                              |
| Max Idle Connections Per Host | `max_idle_conns_per_host` | 50      | ✅             | Maximum idle connections per upstream host                                  |
| Proxy Server                  | `proxy`                   | -       | ✅             | Request proxy server address, leave empty to use environment variable proxy |

#### Key Configuration

| Setting                    | Field Name                        | Default | Group Override | Description                                                                |
| -------------------------- | --------------------------------- | ------- | -------------- | -------------------------------------------------------------------------- |
| Max Retries                | `max_retries`                     | 3       | ✅             | Maximum retry count using different keys for single request                |
| Blacklist Threshold        | `blacklist_threshold`             | 3       | ✅             | Number of consecutive failures before key enters blacklist                 |
| Key Validation Interval    | `key_validation_interval_minutes` | 60      | ✅             | Background scheduled key validation cycle (minutes)                        |
| Key Validation Concurrency | `key_validation_concurrency`      | 10      | ✅             | Concurrency for background validation of invalid keys                      |
| Key Validation Timeout     | `key_validation_timeout_seconds`  | 20      | ✅             | API request timeout for validating individual keys in background (seconds) |

## Web Management Interface

Access the management console at: <http://localhost:3001> (default address)

### Interface Overview

<img src="screenshot/dashboard.png" alt="Dashboard" width="600"/>

<br/>

<img src="screenshot/keys.png" alt="Key Management" width="600"/>

<br/>

The web management interface provides the following features:

- **Dashboard**: Real-time statistics and system status overview
- **Key Management**: Create and configure AI service provider groups, add, delete, and monitor API keys
- **Request Logs**: Detailed request history and debugging information
- **System Settings**: Global configuration management and hot-reload

## API Usage Guide

### Proxy Interface Invocation

GPT-Load routes requests to different AI services through group names. Usage is as follows:

#### 1. Proxy Endpoint Format

```text
http://localhost:3001/proxy/{group_name}/{original_api_path}
```

- `{group_name}`: Group name created in the management interface
- `{original_api_path}`: Maintain complete consistency with original AI service paths

#### 2. Authentication Methods

As a transparent proxy service, GPT-Load completely preserves the native authentication formats of various AI services:

- **OpenAI Format**: Uses `Authorization: Bearer {AUTH_KEY}` header authentication
- **Gemini Format**: Uses URL parameter `key={AUTH_KEY}` authentication
- **Unified Key**: All services use the unified key value configured in the `AUTH_KEY` environment variable

#### 3. OpenAI Interface Example

Assuming a group named `openai` was created:

**Original invocation:**

```bash
curl -X POST https://api.openai.com/v1/chat/completions \
  -H "Authorization: Bearer sk-your-openai-key" \
  -H "Content-Type: application/json" \
  -d '{"model": "gpt-4.1-mini", "messages": [{"role": "user", "content": "Hello"}]}'
```

**Proxy invocation:**

```bash
curl -X POST http://localhost:3001/proxy/openai/v1/chat/completions \
  -H "Authorization: Bearer sk-123456" \
  -H "Content-Type: application/json" \
  -d '{"model": "gpt-4.1-mini", "messages": [{"role": "user", "content": "Hello"}]}'
```

**Changes required:**

- Replace `https://api.openai.com` with `http://localhost:3001/proxy/openai`
- Replace original API Key with unified authentication key `sk-123456` (default value)

#### 4. Gemini Interface Example

Assuming a group named `gemini` was created:

**Original invocation:**

```bash
curl -X POST https://generativelanguage.googleapis.com/v1beta/models/gemini-2.5-pro:generateContent?key=your-gemini-key \
  -H "Content-Type: application/json" \
  -d '{"contents": [{"parts": [{"text": "Hello"}]}]}'
```

**Proxy invocation:**

```bash
curl -X POST http://localhost:3001/proxy/gemini/v1beta/models/gemini-2.5-pro:generateContent?key=sk-123456 \
  -H "Content-Type: application/json" \
  -d '{"contents": [{"parts": [{"text": "Hello"}]}]}'
```

**Changes required:**

- Replace `https://generativelanguage.googleapis.com` with `http://localhost:3001/proxy/gemini`
- Replace `key=your-gemini-key` in URL parameter with unified authentication key `sk-123456` (default value)

#### 5. Supported Interfaces

**OpenAI Format:**

- `/v1/chat/completions` - Chat conversations
- `/v1/completions` - Text completion
- `/v1/embeddings` - Text embeddings
- `/v1/models` - Model list
- And all other OpenAI-compatible interfaces

**Gemini Format:**

- `/v1beta/models/*/generateContent` - Content generation
- `/v1beta/models` - Model list
- And all other Gemini native interfaces

#### 6. Client SDK Configuration

**OpenAI Python SDK:**

```python
from openai import OpenAI

client = OpenAI(
    api_key="sk-123456",  # Use unified authentication key
    base_url="http://localhost:3001/proxy/openai"  # Use proxy endpoint
)

response = client.chat.completions.create(
    model="gpt-4.1-mini",
    messages=[{"role": "user", "content": "Hello"}]
)
```

**Google Gemini SDK (Python):**

```python
import google.generativeai as genai

# Configure API key and base URL
genai.configure(
    api_key="sk-123456",  # Use unified authentication key
    client_options={"api_endpoint": "http://localhost:3001/proxy/gemini"}
)

model = genai.GenerativeModel('gemini-2.5-pro')
response = model.generate_content("Hello")
```

> **Important Note**: As a transparent proxy service, GPT-Load completely preserves the native API formats and authentication methods of various AI services. You only need to replace the endpoint address and use the unified key value for seamless migration.

## License

MIT License - see [LICENSE](LICENSE) file for details.

## Star History

[![Stargazers over time](https://starchart.cc/tbphp/gpt-load.svg?variant=adaptive)](https://starchart.cc/tbphp/gpt-load)
