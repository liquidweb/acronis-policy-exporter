package main

import (
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const namespace = "acronis"

func probeHandler(path targetToCachePathFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		registry := prometheus.NewRegistry()
		var err error

		probeSuccess := prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "probe_success",
			Help: "Boolean if probe was successful",
		})
		if err = registry.Register(probeSuccess); err != nil {
			http.Error(w, "", http.StatusInternalServerError)
			return
		}

		probeDurationGauge := prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "probe_duration_seconds",
			Help: "milliseconds for probe to respond",
		})
		if err = registry.Register(probeDurationGauge); err != nil {
			http.Error(w, "", http.StatusInternalServerError)
			return
		}

		target := tgtStr(r.URL.Query().Get("target"))

		task, err := readTask(path(target))

		if err != nil {
			task.Result.Code = "nomatch"
			probeSuccess.Set(0)
		} else {
			probeSuccess.Set(1)
			if err = taskToRegistry(registry, task); err != nil {
				http.Error(w, "", http.StatusInternalServerError)
				return
			}
		}

		if err = registry.Register(registerPolicyState(task)); err != nil {
			http.Error(w, "", http.StatusInternalServerError)
			return
		}

		probeDurationGauge.Set(float64(time.Since(start).Milliseconds()))
		promhttp.HandlerFor(registry, promhttp.HandlerOpts{}).ServeHTTP(w, r)
	})
}

func registerPolicyState(task Task) prometheus.Gauge {
	policyState := prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "policy_state",
		Help:      "OK=0 WARNING=1 ERROR=2 UNKNOWN=3",
	})

	if code, ok := map[string]int{
		"ok":      0,
		"warning": 1,
		"error":   2,
	}[task.Result.Code]; ok {
		policyState.Set(float64(code))
	} else {
		policyState.Set(float64(3))
	}
	return policyState
}

func taskToRegistry(registry *prometheus.Registry, task Task) error {
	metadata := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "policy_info",
			Help:      "Metadata Info of policy",
		}, []string{
			"tenantId",
			"tenantName",
			"policyType",
			"policyId",
			"policyName",
			"machineName",
		},
	).WithLabelValues(
		task.Tenant.ID,
		task.Tenant.Name,
		task.Policy.Type,
		task.Policy.ID,
		task.Policy.Name,
		task.Context.MachineName,
	)
	metadata.Set(1)

	policyError := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "policy_error",
			Help:      "Error from last run of policy",
		},
		[]string{
			"reason",
			"cause",
			"effect",
		},
	).WithLabelValues(
		task.Result.Error.Reason,
		task.Result.Error.Context.Cause,
		task.Result.Error.Context.Effect,
	)
	policyError.Set(1)

	lastRun := prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "lastrun_timestamp",
			Help:      "Timestamp of last task run",
		})
	lastRun.Set(float64(task.Updated.Unix()))

	for _, gauge := range []prometheus.Collector{metadata, policyError, lastRun} {
		err := registry.Register(gauge)
		if err != nil {
			return err
		}
	}
	return nil
}
