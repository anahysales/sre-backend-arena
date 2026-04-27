# SRE Backend Arena — Architecture & System Design

## 1. Visão Geral

O sistema **Death Star Analysis API** é uma aplicação escrita em Go projetada para análise de starships com base em dados externos da SWAPI, com foco em:

* Resiliência sob carga
* Observabilidade completa
* Controle de latência
* Simulação de falhas em dependências externas
* Execução em ambiente Kubernetes (k3d)

O sistema foi projetado como um desafio de SRE, simulando cenários reais de degradação de serviços dependentes.

---

## 2. Arquitetura Geral

### Componentes principais

* **API Go (deathstar-api)**
* **SWAPI (dependência externa)**
* **Kubernetes (k3d cluster local)**
* **Prometheus (coleta de métricas)**
* **Grafana (visualização de SLOs e métricas)**
* **k6 (load testing)**

### Fluxo de requisição

```
Client → NodePort / Port-forward → Go API → Cache → SWAPI → Response
```

---

## 3. API (Implementação Go)

### Endpoint principal

```
GET /deathstar-analysis/{ship_id}
```

### Responsabilidades:

* Consulta de dados na SWAPI
* Cálculo de threat score
* Classificação de risco da nave
* Cache de respostas
* Emissão de métricas e logs estruturados

---

## 4. Cache

Implementado como cache **in-memory thread-safe**:

* `sync.RWMutex` para concorrência segura
* TTL de 30 segundos
* Key baseada em `ship_id`

### Objetivo:

* Reduzir chamadas à SWAPI
* Melhorar performance em cache hits
* Diminuir variabilidade sob carga

---

## 5. Estratégia de Retry e Resiliência

### Configuração atual:

* Timeout HTTP: **3 segundos**
* Retry: **2 tentativas**
* Backoff: linear (200ms × tentativa)

### Comportamento:

* Em falha da SWAPI, o sistema tenta novamente antes de falhar
* Após esgotar tentativas, retorna `502 Bad Gateway`

### Limitação atual:

* Não há circuit breaker implementado
* Dependência forte da SWAPI sob carga

---

## 6. Observabilidade

### Métricas (Prometheus)

* `http_requests_total`
* `http_request_duration_seconds`
* `cache_hits_total`
* `cache_miss_total`

### Logs estruturados

Formato JSON com:

* `trace_id`
* `ship_id`
* `event`
* `timestamp`
* `extra metadata`

### Objetivo:

* Rastreabilidade ponta a ponta
* Análise de performance por requisição
* Debug distribuído

---

## 7. SLOs e Monitoramento

O sistema define métricas de qualidade:

* Latência (p95)
* Taxa de erro
* Taxa de tráfego
* Performance de cache

Alertas são definidos via Prometheus rules.

---

## 8. Infraestrutura

### Kubernetes (k3d)

* Deployment do serviço Go
* Service ClusterIP + NodePort
* ServiceMonitor para Prometheus

### Acesso externo

* Port-forward usado para testes locais
* NodePort configurado para exposição do serviço

---

## 9. Load Testing

Ferramenta: **k6**

### Cenário atual:

* até 50 VUs
* ramp-up progressivo
* duração ~2 minutos

### Resultados observados:

* Alta taxa de erro sob carga (~95%)
* Latência p95 elevada (~18s)
* Gargalo identificado na dependência externa (SWAPI)

---

## 10. Limitações Conhecidas

* Circuit breaker não implementado
* Retry não exponencial (linear)
* SWAPI é gargalo crítico sob carga
* Concorrência externa não limitada agressivamente
* Latência elevada sob stress

---

## 11. Próximos Passos (Roadmap Técnico)

* Implementação de circuit breaker
* Fallback de resposta degradada
* Otimização de concorrência externa
* Redução de latência p95 para <500ms
* Melhoria de throughput para 8k–10k RPS

---

## 12. Conclusão

O sistema já possui boa base de:

* Observabilidade
* Cache
* Instrumentação
* Estrutura em Kubernetes

O principal gap atual está na **resiliência avançada sob falha de dependência externa**, que será o foco da próxima evolução.
