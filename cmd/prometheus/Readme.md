Build: `cd cmd/prometheus && docker-compose build --parallel`

Run only Prometheus: `cd cmd/prometheus && docker-compose up prometheus grafana`

Run with TurboGeth, RestApi and DebugUI: `cd cmd/prometheus && TGETH_DATADIR=/path/to/geth/data/dir docker-compose up`

Grafana: [localhost:3000](localhost:3000), admin/admin
DebugUI: [localhost:3001](localhost:3001)

