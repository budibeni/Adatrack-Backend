FROM prom/prometheus:v2.45.0
COPY ./deployments/prometheus/prometheus.yml /etc/prometheus/prometheus.yml
COPY ./deployments/prometheus/alert_rules.yml /etc/prometheus/alert_rules.yml
COPY ./deployments/prometheus/targets /etc/prometheus/targets
