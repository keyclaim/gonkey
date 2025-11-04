package allure_report

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/lamoda/gonkey/models"
)

// mockInfo contains information about a mock service
type mockInfo struct {
	ServiceName string
	Strategy    string
	Endpoints   []string
}

// extractMockInfo extracts structured information about mock services
func extractMocksInfo(mocks map[string]interface{}) []mockInfo {
	if len(mocks) == 0 {
		return nil
	}

	var infos []mockInfo
	for serviceName, def := range mocks {
		info := mockInfo{
			ServiceName: serviceName,
		}

		defMap, ok := def.(map[interface{}]interface{})
		if !ok {
			info.Strategy = "unknown"
			infos = append(infos, info)
			continue
		}

		if s, ok := defMap["strategy"]; ok {
			if strategyStr, ok := s.(string); ok {
				info.Strategy = strategyStr
				info.Endpoints = extractEndpoints(strategyStr, defMap)
			}
		}

		infos = append(infos, info)
	}

	sort.Slice(infos, func(i, j int) bool {
		return infos[i].ServiceName < infos[j].ServiceName
	})

	return infos
}

// extractEndpoints extracts endpoint information based on strategy
func extractEndpoints(strategy string, def map[interface{}]interface{}) []string {
	var endpoints []string

	switch strategy {
	case "constant", "template":
		if path, ok := def["path"].(string); ok {
			endpoints = []string{path}
		} else {
			endpoints = []string{"/"}
		}

	case "file":
		if filename, ok := def["filename"].(string); ok {
			endpoints = []string{fmt.Sprintf("file: %s", filepath.Base(filename))}
		}

	case "uriVary":
		if uris, ok := def["uris"].(map[interface{}]interface{}); ok {
			for uri := range uris {
				if uriStr, ok := uri.(string); ok {
					endpoints = append(endpoints, uriStr)
				}
			}
			sort.Strings(endpoints)
		}

		if basePath, ok := def["basePath"].(string); ok && basePath != "" {
			for i, ep := range endpoints {
				endpoints[i] = basePath + ep
			}
		}

	case "methodVary":
		if methods, ok := def["methods"].(map[interface{}]interface{}); ok {
			for method := range methods {
				if methodStr, ok := method.(string); ok {
					endpoints = append(endpoints, methodStr)
				}
			}
			sort.Strings(endpoints)
		}

	case "basedOnRequest":
		if uris, ok := def["uris"].([]interface{}); ok {
			for _, uriDef := range uris {
				if uriMap, ok := uriDef.(map[interface{}]interface{}); ok {
					if path, ok := uriMap["path"].(string); ok {
						endpoints = append(endpoints, path)
					} else {
						endpoints = append(endpoints, "[conditional]")
					}
				}
			}
		}

	case "sequence":
		if seq, ok := def["sequence"].([]interface{}); ok {
			endpoints = []string{fmt.Sprintf("[sequence: %d steps]", len(seq))}
		}

	case "nop", "dropRequest":
		endpoints = []string{"-"}
	}

	return endpoints
}

// formatFixturesInfo formats fixture information for display
func formatFixturesInfo(fixtures []string, multiDb models.FixturesMultiDb) string {
	if len(fixtures) > 0 {
		return fmt.Sprintf("%s (%d files)", strings.Join(fixtures, ", "), len(fixtures))
	}

	if len(multiDb) > 0 {
		var parts []string
		for _, fixture := range multiDb {
			parts = append(parts, fmt.Sprintf("%s: %d files", fixture.DbName, len(fixture.Files)))
		}
		return strings.Join(parts, "; ")
	}

	return ""
}
