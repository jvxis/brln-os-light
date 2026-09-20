package privileged

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"lightningos-light/internal/appmanifest"
)

const catalogStopInspectFormat = `{"id":{{json .Id}},"labels":{{json .Config.Labels}}}`

var catalogStopIDPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
var catalogStopServicePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)

type catalogStopContainer struct {
	ID     string            `json:"id"`
	Labels map[string]string `json:"labels"`
}

// stopCatalogApp never loads Compose, credentials or image references from an
// installed release. Docker's project/service labels identify the existing
// runtime, including sidecars and services removed from newer catalogs. Only
// fixed catalog projects and re-inspected full container IDs reach docker stop.
// Start remains a separate, strictly validated current-catalog operation.
func stopCatalogApp(ctx context.Context, runner CommandRunner, appID string, dryRun bool) error {
	manifest, err := appmanifest.ComposeManifestForApp(appID)
	if err != nil || runner == nil {
		return errors.New("app stop catalog is unavailable")
	}
	if dryRun {
		return nil
	}
	listArgs := []string{"ps", "--no-trunc", "--filter", "label=com.docker.compose.project=" + manifest.Project,
		"--filter", "label=com.docker.compose.oneoff=False", "--format", "{{.ID}}"}
	raw, err := runner.Run(ctx, dockerPath, listArgs...)
	if err != nil {
		return errors.New("app stop container lookup failed")
	}
	if len(raw) > 64*1024 {
		return errors.New("app stop container inventory is too large")
	}
	ids := strings.Fields(raw)
	if len(ids) > 64 {
		return errors.New("app stop container inventory is too large")
	}
	seen := map[string]bool{}
	containers := make(map[string][]string)
	dependencies := make(map[string][]string)
	for _, id := range ids {
		if !catalogStopIDPattern.MatchString(id) || seen[id] {
			return errors.New("app stop container identity is invalid")
		}
		seen[id] = true
		out, err := runner.Run(ctx, dockerPath, "inspect", "--type", "container", "--format", catalogStopInspectFormat, id)
		if err != nil || len(out) > 64*1024 {
			return errors.New("app stop container verification failed")
		}
		var c catalogStopContainer
		if json.Unmarshal([]byte(out), &c) != nil || c.ID != id ||
			c.Labels["com.docker.compose.project"] != manifest.Project ||
			!strings.EqualFold(c.Labels["com.docker.compose.oneoff"], "false") {
			return errors.New("app stop container ownership is invalid")
		}
		service := c.Labels["com.docker.compose.service"]
		if !catalogStopServicePattern.MatchString(service) {
			return errors.New("app stop service identity is invalid")
		}
		containers[service] = append(containers[service], id)
		depends := c.Labels["com.docker.compose.depends_on"]
		if depends == "" {
			// Compose v1 did not persist dependency labels. Keep the known stack
			// ordering without making old image versions part of authorization.
			dependencies[service] = append(dependencies[service], catalogStopLegacyDependencies(appID, service)...)
		} else {
			for _, entry := range strings.Split(depends, ",") {
				name, _, _ := strings.Cut(entry, ":")
				if !catalogStopServicePattern.MatchString(name) {
					return errors.New("app stop dependency metadata is invalid")
				}
				dependencies[service] = append(dependencies[service], name)
			}
		}
	}
	// Validate the entire inventory and dependency graph before stopping anything.
	order, err := catalogStopOrder(containers, dependencies)
	if err != nil {
		return err
	}
	for _, service := range order {
		args := append([]string{"stop", "--time", strconv.Itoa(manifest.StopTimeoutSeconds)}, containers[service]...)
		if _, err := runner.Run(ctx, dockerPath, args...); err != nil {
			return errors.New("app stop command failed")
		}
	}
	if len(ids) != 0 {
		remaining, err := runner.Run(ctx, dockerPath, listArgs...)
		if err != nil || strings.TrimSpace(remaining) != "" {
			return errors.New("app stop could not confirm all services stopped")
		}
	}
	return nil
}

func catalogStopOrder(containers map[string][]string, dependencies map[string][]string) ([]string, error) {
	services := make([]string, 0, len(containers))
	for service := range containers {
		services = append(services, service)
	}
	sort.Strings(services)
	state := map[string]int{}
	var order []string
	var visit func(string) error
	visit = func(service string) error {
		if state[service] == 1 {
			return errors.New("app stop dependency graph has a cycle")
		}
		if state[service] == 2 || len(containers[service]) == 0 {
			return nil
		}
		state[service] = 1
		for _, dependency := range dependencies[service] {
			if err := visit(dependency); err != nil {
				return err
			}
		}
		state[service] = 2
		order = append(order, service)
		return nil
	}
	for _, service := range services {
		if err := visit(service); err != nil {
			return nil, err
		}
	}
	// Stop dependents first, databases and other dependencies last.
	for i, j := 0, len(order)-1; i < j; i, j = i+1, j-1 {
		order[i], order[j] = order[j], order[i]
	}
	return order, nil
}

func catalogStopLegacyDependencies(appID, service string) []string {
	switch appID + "/" + service {
	case appmanifest.BTCPayID + "/btcpayserver":
		return []string{"nbxplorer"}
	case appmanifest.BTCPayID + "/nbxplorer":
		return []string{"btcpay-db", "tor"}
	case appmanifest.LNDgID + "/lndg":
		return []string{"lndg-db"}
	case appmanifest.MempoolID + "/mempool-web":
		return []string{"mempool-api"}
	case appmanifest.MempoolID + "/mempool-api":
		return []string{"mempool-db"}
	case appmanifest.PublicPoolID + "/public-pool-ui":
		return []string{"public-pool"}
	case appmanifest.BarkWalletID + "/proxy":
		return []string{"web", "api"}
	case appmanifest.BarkWalletID + "/web":
		return []string{"api"}
	case appmanifest.BarkWalletID + "/api":
		return []string{"barkd"}
	case appmanifest.BRLNCommunityID + "/proxy":
		return []string{"web", "signer"}
	case appmanifest.RoboSatsID + "/proxy":
		return []string{"robosats"}
	case appmanifest.RoboSatsID + "/robosats":
		return []string{"tor"}
	}
	return nil
}
