APP_NAME=deathstar-api
HELM_CHART=helm/deathstar-api
K6_SCRIPT=scripts/loadtest.js
NAMESPACE=default
MONITORING_NAMESPACE=monitoring
LOCAL_PORT=8081
SERVICE_PORT=80

.PHONY: help run test coverage build docker-build helm-template helm-dry-run helm-install helm-uninstall deploy port-forward prometheus grafana loadtest pods svc logs status clean validate all

help:
	@echo "Comandos disponíveis:"
	@echo "  make run              - roda a API localmente"
	@echo "  make test             - roda todos os testes"
	@echo "  make coverage         - gera relatório de cobertura"
	@echo "  make build            - compila a aplicação"
	@echo "  make docker-build     - builda a imagem Docker"
	@echo "  make helm-template    - renderiza os manifests Helm"
	@echo "  make helm-dry-run     - valida instalação Helm sem aplicar"
	@echo "  make helm-install     - instala/atualiza via Helm"
	@echo "  make helm-uninstall   - remove release Helm"
	@echo "  make deploy           - builda imagem e faz deploy via Helm"
	@echo "  make port-forward     - expõe a API em localhost:8081"
	@echo "  make prometheus       - expõe Prometheus em localhost:9091"
	@echo "  make grafana          - expõe Grafana em localhost:3000"
	@echo "  make loadtest         - roda teste de carga com k6"
	@echo "  make pods             - lista pods da aplicação"
	@echo "  make svc              - mostra service da aplicação"
	@echo "  make logs             - acompanha logs da aplicação"
	@echo "  make status           - mostra status geral"
	@echo "  make clean            - remove binário local"
	@echo "  make validate         - roda validações principais"
	@echo "  make all              - fluxo completo local de validação"

run:
	go run ./cmd/api

test:
	go test ./...

coverage:
	go test ./... -coverprofile=coverage.out
	go tool cover -func=coverage.out

build:
	mkdir -p bin
	go build -o bin/api ./cmd/api

docker-build:
	docker build -t $(APP_NAME):latest .

helm-template:
	helm template $(APP_NAME) $(HELM_CHART)

helm-dry-run:
	helm install $(APP_NAME) $(HELM_CHART) --dry-run=client

helm-install:
	helm upgrade --install $(APP_NAME) $(HELM_CHART) -n $(NAMESPACE)

helm-uninstall:
	helm uninstall $(APP_NAME) -n $(NAMESPACE)

deploy:
	@echo ">> buildando imagem docker"
	docker build -t $(APP_NAME):latest .
	@echo ">> deploy via helm"
	helm upgrade --install $(APP_NAME) $(HELM_CHART) -n $(NAMESPACE)

port-forward:
	kubectl port-forward -n $(NAMESPACE) svc/$(APP_NAME) $(LOCAL_PORT):$(SERVICE_PORT)

prometheus:
	kubectl port-forward -n $(MONITORING_NAMESPACE) svc/prometheus-operated 9091:9090

grafana:
	kubectl port-forward -n $(MONITORING_NAMESPACE) svc/monitoring-grafana 3000:80

loadtest:
	k6 run $(K6_SCRIPT)

pods:
	kubectl get pods -n $(NAMESPACE) -l app=$(APP_NAME)

svc:
	kubectl get svc -n $(NAMESPACE) $(APP_NAME)

logs:
	kubectl logs -n $(NAMESPACE) -l app=$(APP_NAME) -f

status:
	kubectl get pods -n $(NAMESPACE) -l app=$(APP_NAME)
	kubectl get svc -n $(NAMESPACE) $(APP_NAME)
	kubectl get hpa -n $(NAMESPACE) $(APP_NAME)
	kubectl get pdb -n $(NAMESPACE) $(APP_NAME)
	kubectl get prometheusrule -n $(NAMESPACE) $(APP_NAME)-rules
	kubectl get servicemonitor -n $(MONITORING_NAMESPACE) $(APP_NAME)

clean:
	rm -rf bin

validate:
	make test
	make helm-template
	make helm-dry-run

all:
	make clean
	make test
	make build
	make docker-build
	make helm-template
	make helm-dry-run