[README.md](https://github.com/user-attachments/files/27156218/README.md)
# ☠️ Death Star Threat Analysis API

> Solução para o **Cenário 2** do desafio [SRE Backend Arena](https://github.com/kailimadev/sre-backend-arena)

API REST em Go que analisa o nível de ameaça de naves da saga Star Wars consultando a [SWAPI](https://swapi.py4e.com/), calculando um `threatScore` com base na tripulação e passageiros da nave.

---

## 🏗️ Arquitetura

```
Cliente HTTP
     │
     ▼
┌──────────────────────────────────────┐
│  GET /deathstar-analysis/{shipId}    │
│  - Valida método e path              │
│  - Gera X-Trace-Id (UUID v4)         │
│  - Registra métricas Prometheus      │
└──────────────┬───────────────────────┘
               │
       ┌───────▼────────┐
       │  Cache Check   │  TTL: 30s · sync.RWMutex
       └───────┬────────┘
               │
        hit ◄──┴──► miss
         │               │
         ▼               ▼
    Resposta       Circuit Breaker
    imediata            │
                  OPEN ─┴─ CLOSED
                         │
                   Rate Limiter
                   (100ms token)
                         │
                   Semáforo (20)
                         │
                    SWAPI Call
                   timeout: 3s
                   retry: 3x + jitter
                         │
                  ┌──────┴──────┐
                OK              Falha
                 │               │
          Salva Cache       Fallback
                 │          degraded: true
                 └─────┬────────┘
                        ▼
                  service.CalculateThreat()
                        │
                        ▼
                  Resposta JSON
```

---

## ⚙️ Decisões de Design

### Linguagem: Go
Go foi escolhido pelo modelo de concorrência nativo (goroutines + channels), baixo overhead de memória e performance próxima ao C — essencial para sustentar 8k–10k RPS dentro do orçamento de CPU/memória definido no Deployment K8s.

### Cache in-memory com sync.RWMutex
- **TTL de 30 segundos** — equilibra frescor dos dados com redução de chamadas à SWAPI
- **sync.RWMutex** — múltiplas leituras simultâneas sem bloqueio, exclusividade apenas na escrita
- **cloneMap()** — cópia defensiva do map ao salvar e servir do cache, evitando race conditions
- Alternativa considerada: Redis — descartada para não introduzir dependência externa, dado que os dados da SWAPI são essencialmente estáticos

### Backoff exponencial com jitter
3 tentativas com espera exponencial + jitter aleatório:
- Tentativa 1: 100ms + rand(100ms)
- Tentativa 2: 200ms + rand(100ms)
- Tentativa 3: 400ms + rand(100ms)

O jitter evita thundering herd quando múltiplas instâncias falham simultaneamente.

### Circuit Breaker
Implementado do zero sem biblioteca externa:
- **CLOSED** → operação normal
- **OPEN** → bloqueia chamadas após 5 falhas consecutivas, por 10 segundos
- **Auto-reset** → após o timeout, volta ao estado CLOSED e tenta recuperação

Garante que a API não fique travada esperando timeouts da SWAPI sob falha prolongada.

### Fallback degradado
Quando a SWAPI está indisponível (circuit breaker aberto ou retries esgotados), a API retorna uma resposta válida com `degraded: true` em vez de devolver 502 ao cliente. Isso mantém o serviço disponível mesmo sob falha da dependência externa.

### Rate Limiting client-side
Token bucket de 100ms entre chamadas à SWAPI, combinado com semáforo de 20 goroutines simultâneas — respeita o limite da API externa e evita bloqueios por `429 Too Many Requests`.

### Observabilidade com métricas granulares
Além das métricas padrão de HTTP, a API expõe contadores específicos para cada comportamento de resiliência:

| Métrica | O que mede |
|--------|-----------|
| `cache_hits_total` | Eficiência do cache |
| `cache_miss_total` | Frequência de chamadas externas |
| `fallback_total` | Quantas vezes o fallback foi ativado |
| `circuit_open_total` | Quantas vezes o circuit breaker bloqueou |
| `retry_total` | Total de retentativas feitas |
| `external_rate_limited_total` | Chamadas controladas pelo rate limiter |
| `degraded_responses_total` | Respostas entregues em modo degradado |

---

## 📊 SLOs

| SLO | Meta | Janela | Alerta |
|-----|------|--------|--------|
| Taxa de sucesso | ≥ 99% | 5 min | `DeathstarFastErrorBurn` (> 10%) |
| Burn rate lento | ≥ 95% | 5 min | `DeathstarSlowErrorBurn` (> 5%) |
| Latência p95 | < 2s | 5 min | `DeathstarHighLatency` |
| Error budget | ≥ 50% restante | 5 min | `DeathstarErrorBudgetBurning` |

---

## 🚀 Como executar

### Docker Compose (desenvolvimento local)
```bash
docker compose up --build
```

A API estará disponível em `http://localhost:8081`.
Prometheus: `http://localhost:9090` · Grafana: `http://localhost:3000`

### Kubernetes (k3d)
```bash
# Subir cluster local
k3d cluster create deathstar

# Deploy da stack completa
kubectl apply -f infra/k8s/
kubectl apply -f infra/prometheus/

# Acessar a API
kubectl port-forward svc/deathstar-api 8081:8081
```

### Exemplo de uso
```bash
# Nave normal
curl http://localhost:8081/deathstar-analysis/9

# Health check
curl http://localhost:8081/health

# Métricas Prometheus
curl http://localhost:8081/metrics
```

### Resposta de exemplo
```json
{
  "ship": "Death Star",
  "model": "DS-1 Orbital Battle Station",
  "crew": "342,953",
  "passengers": "843,342",
  "threatScore": 100,
  "class": "galactic_superweapon",
  "degraded": false
}
```

### Resposta em modo degradado (SWAPI indisponível)
```json
{
  "ship": "unknown",
  "model": "unknown",
  "crew": "0",
  "passengers": "0",
  "threatScore": 0,
  "class": "low_threat",
  "degraded": true
}
```

---

## 🧠 Classificação de ameaça

| threatScore | Classificação |
|-------------|---------------|
| 0 – 19 | `low_threat` |
| 20 – 49 | `medium_threat` |
| 50 – 79 | `high_threat` |
| 80 – 100 | `galactic_superweapon` |

> **Fórmula:** `threatScore = min((crew + passengers) / 10.000, 100)`

---

## 🧪 Testes

```bash
# Todos os testes (unitários + integração)
go test ./... -v -cover

# Apenas serviço
go test ./internal/service/... -v -cover

# Apenas integração
go test ./cmd/api/... -v -cover
```

Os testes de integração usam `httptest.NewServer()` para mockar a SWAPI — nenhuma chamada real é feita durante os testes.

Cobertura mínima exigida: **≥ 70%**

---

## 📁 Estrutura do projeto

```
.
├── cmd/api/
│   ├── main.go                    # Entry point, handlers, circuit breaker, cache
│   └── main_integration_test.go   # Testes de integração com mock da SWAPI
├── internal/
│   └── service/
│       ├── threat.go              # Lógica de calculateThreat (isolada e testável)
│       └── service_test.go        # Testes unitários de CalculateThreat
├── infra/
│   ├── k8s/
│   │   ├── deployment.yaml        # 2 réplicas, RollingUpdate, probes
│   │   ├── service.yaml           # ClusterIP + NodePort
│   │   ├── hpa.yaml               # HPA: 2–5 réplicas por CPU
│   │   ├── pdb.yaml               # PodDisruptionBudget (minAvailable: 1)
│   │   ├── servicemonitor.yaml    # Integração Prometheus Operator
│   │   ├── prometheusrule.yaml    # Alertas como objeto K8s nativo
│   │   └── grafana/
│   │       ├── configmap.yaml     # Dashboard Grafana como ConfigMap
│   │       ├── datasource.yaml    # Datasource Prometheus como código
│   │       └── deathstar-dashboard.json
│   └── prometheus/
│       ├── prometheus.yml         # Configuração do Prometheus
│       ├── rules.yml              # Recording rules
│       ├── alerts.yml             # Alertas fast/slow burn
│       └── slo.yml                # SLOs como PrometheusRule
├── scripts/
│   └── loadtest.js                # Load test k6 com ramp-up progressivo
├── docs/
│   └── architecture.md            # Decisões de design e limitações
├── Dockerfile                     # Multi-stage build
└── docker-compose.yml             # Stack local: API + Prometheus + Grafana
```

---

## 🏆 Achievements Desbloqueados

| # | Achievement | Descrição | Evidência |
|---|-------------|-----------|-----------|
| ⚡ | **Rate Limit Guardian** | Respeita o rate limit da SWAPI sem receber 429 | Token bucket de 100ms + semáforo de 20 goroutines em `getStarshipInfo()` |
| 🛡️ | **Circuit Breaker** | Circuit breaker implementado do zero | `CircuitBreaker` struct com estados CLOSED/OPEN, threshold 5 falhas, timeout 10s |
| 📦 | **Cache Master** | Cache in-memory thread-safe com TTL | `sync.RWMutex` + TTL 30s + `cloneMap()` defensivo |
| 🌊 | **Graceful Degradation** | Fallback que mantém o serviço disponível | `degraded: true` na resposta quando SWAPI falha |
| 🔁 | **Retry Resilience** | Backoff exponencial com jitter | `100*(1<<i)ms + rand(100ms)` em até 3 tentativas |
| 🔍 | **Trace Propagator** | Rastreabilidade completa por request | Header `X-Trace-Id` (UUID v4) em todas as respostas + logs estruturados |
| 📊 | **SLO Guardian** | SLOs definidos com alertas automáticos | `alerts.yml` + `slo.yml` + `prometheusrule.yaml` com fast/slow burn |
| 📈 | **Grafana Observer** | Dashboard de observabilidade como código | `configmap.yaml` + `datasource.yaml` no K8s |
| 🚀 | **K8s Production Ready** | Deploy com alta disponibilidade | HPA (2–5 réplicas), PDB, RollingUpdate com maxUnavailable: 0, 3 tipos de probe |
| 🧪 | **Test Coverage** | Cobertura ≥ 70% com testes de integração reais | `main_integration_test.go` com mock da SWAPI via `httptest` |

---

## 🔗 Endpoints

| Método | Path | Descrição |
|--------|------|-----------|
| `GET` | `/deathstar-analysis/{shipId}` | Análise de ameaça da nave |
| `GET` | `/health` | Health check da API |
| `GET` | `/metrics` | Métricas Prometheus |
