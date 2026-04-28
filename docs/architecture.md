# SRE Backend Arena — Architecture & System Design

## 1. Visão geral

A **Death Star Analysis API** é um serviço em Go que simula análise de naves com base em dados da SWAPI.

O foco do projeto é testar padrões de SRE em um cenário controlado:

- comportamento sob carga
- falhas em dependências externas
- observabilidade completa
- controle de latência
- execução em Kubernetes local (k3d)

---

## 2. Arquitetura

### Componentes

- API em Go (`deathstar-api`)
- SWAPI (dependência externa)
- Kubernetes (k3d local)
- Prometheus (métricas)
- Grafana (dashboards e SLOs)
- k6 (load testing)

### Fluxo

Client → Service → Cache → SWAPI → Response

---

## 3. API

### Endpoint

GET /deathstar-analysis/{ship_id}

### O que a API faz

- busca dados da nave na SWAPI
- calcula threat score
- classifica nível de ameaça
- aplica cache local
- expõe métricas e logs estruturados

---

## 4. Cache

Cache em memória com proteção de concorrência:

- sync.RWMutex
- TTL de 30s
- chave baseada em ship_id

### Objetivo

- reduzir chamadas na SWAPI
- melhorar latência em cache hit
- estabilizar carga sob stress

---

## 5. Retry e resiliência

Configuração atual:

- timeout HTTP: 3s
- até 2 tentativas
- backoff linear (200ms por tentativa)

### Comportamento

- falha na SWAPI → retry automático
- esgotou tentativas → retorna 502

### Limitação atual

- ainda não há circuit breaker
- dependência forte da SWAPI sob carga

---

## 6. Observabilidade

### Métricas (Prometheus)

- http_requests_total
- http_request_duration_seconds
- cache_hits_total
- cache_miss_total

### Logs

Logs estruturados em JSON contendo:

- trace_id
- ship_id
- event
- timestamp
- metadados adicionais

---

## 7. SLOs e monitoramento

As métricas principais monitoradas:

- latência (p95)
- taxa de erro
- throughput
- eficiência de cache

Alertas são definidos via Prometheus Rules.

---

## 8. Infraestrutura

### Kubernetes (k3d)

- deployment da API
- service ClusterIP + NodePort
- integração com Prometheus

### Acesso

- port-forward para desenvolvimento local
- NodePort para testes externos

---

## 9. Load testing

Ferramenta: k6

### Cenário atual

- até 50 VUs
- ramp-up progressivo
- duração ~2 minutos

### Resultado observado

- alta taxa de erro sob carga (~95%)
- latência p95 alta (~18s)
- gargalo principal: SWAPI

---

## 10. Limitações atuais

- não há circuit breaker
- retry linear (não exponencial)
- SWAPI é ponto único de falha
- concorrência externa ainda agressiva
- latência instável sob carga

---

## 11. Próximos passos

- circuit breaker
- fallback degradado
- controle de concorrência mais fino
- redução de p95 (<500ms)
- aumento de throughput (8k–10k RPS)

---

## 12. Resumo

Hoje o sistema já cobre bem:

- observabilidade
- cache
- instrumentação
- execução em k8s

O principal ponto de evolução está na resiliência contra falha de dependência externa, que é onde entram circuit breaker e fallback.