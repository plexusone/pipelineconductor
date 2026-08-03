// Package dashboard provides dashboard generation from compliance results.
package dashboard

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/plexusone/pipelineconductor/pkg/model"
	"github.com/plexusone/uiforge/dashboardir"
)

// GenerateComplianceDashboard creates a UIForge dashboard from a CheckResult.
func GenerateComplianceDashboard(result *model.CheckResult, dataURL string) *dashboardir.Dashboard {
	// Build title from config
	var sources []string
	if len(result.Config.Orgs) > 0 {
		sources = append(sources, fmt.Sprintf("orgs: %s", strings.Join(result.Config.Orgs, ", ")))
	}
	if len(result.Config.Users) > 0 {
		sources = append(sources, fmt.Sprintf("users: %s", strings.Join(result.Config.Users, ", ")))
	}

	title := "Workflow Compliance Dashboard"
	description := fmt.Sprintf("Compliance check for %s | Languages: %s | Reference: %s@%s",
		strings.Join(sources, "; "),
		strings.Join(result.Config.Languages, ", "),
		result.Config.RefRepo,
		result.Config.RefBranch,
	)

	dashboard := &dashboardir.Dashboard{
		ID:          "compliance-dashboard",
		Title:       title,
		Description: description,
		Version:     "1.0.0",
		Layout: dashboardir.Layout{
			Type:      dashboardir.LayoutTypeGrid,
			Columns:   12,
			RowHeight: 80,
			Gap:       16,
			Padding:   24,
		},
		DataSources: buildDataSources(result, dataURL),
		Widgets:     buildWidgets(result),
		Theme: &dashboardir.Theme{
			Mode:         dashboardir.ThemeModeLight,
			PrimaryColor: "#3b82f6",
		},
	}

	return dashboard
}

func buildDataSources(_ *model.CheckResult, dataURL string) []dashboardir.DataSource {
	dataSources := []dashboardir.DataSource{
		{
			ID:     "compliance-data",
			Name:   "Compliance Results",
			Type:   dashboardir.DataSourceTypeURL,
			URL:    dataURL,
			Format: dashboardir.DataFormatJSON,
		},
	}

	// Add derived data sources for convenience
	dataSources = append(dataSources, dashboardir.DataSource{
		ID:          "summary",
		Name:        "Summary",
		Type:        dashboardir.DataSourceTypeDerived,
		DerivedFrom: "compliance-data",
		Transform: []dashboardir.Transform{
			{
				Type:   dashboardir.TransformTypeExtract,
				Config: mustJSON(dashboardir.ExtractConfig{Path: "summary"}),
			},
		},
	})

	dataSources = append(dataSources, dashboardir.DataSource{
		ID:          "language-stats",
		Name:        "Language Statistics",
		Type:        dashboardir.DataSourceTypeDerived,
		DerivedFrom: "compliance-data",
		Transform: []dashboardir.Transform{
			{
				Type:   dashboardir.TransformTypeExtract,
				Config: mustJSON(dashboardir.ExtractConfig{Path: "summary.byLanguage"}),
			},
		},
	})

	dataSources = append(dataSources, dashboardir.DataSource{
		ID:          "repos",
		Name:        "Repositories",
		Type:        dashboardir.DataSourceTypeDerived,
		DerivedFrom: "compliance-data",
		Transform: []dashboardir.Transform{
			{
				Type:   dashboardir.TransformTypeExtract,
				Config: mustJSON(dashboardir.ExtractConfig{Path: "repos"}),
			},
		},
	})

	// Non-compliant repos
	dataSources = append(dataSources, dashboardir.DataSource{
		ID:          "non-compliant-repos",
		Name:        "Non-Compliant Repositories",
		Type:        dashboardir.DataSourceTypeDerived,
		DerivedFrom: "repos",
		Transform: []dashboardir.Transform{
			{
				Type: dashboardir.TransformTypeFilter,
				Config: mustJSON(dashboardir.FilterConfig{
					Field:    "complianceLevel",
					Operator: dashboardir.FilterOpNotEqual,
					Value:    "full",
				}),
			},
			{
				Type: dashboardir.TransformTypeSort,
				Config: mustJSON(dashboardir.SortConfig{
					Field:     "complianceLevel",
					Direction: dashboardir.SortDirectionAsc,
				}),
			},
		},
	})

	return dataSources
}

func buildWidgets(_ *model.CheckResult) []dashboardir.Widget {
	widgets := []dashboardir.Widget{}

	// Row 1: Key metrics (4 metrics across)
	widgets = append(widgets,
		buildMetricWidget("total-repos", "Total Repositories", 0, 0, 2,
			"totalRepos", "number", nil),
		buildMetricWidget("compliant", "Fully Compliant", 3, 0, 2,
			"compliantRepos", "number", []dashboardir.MetricThreshold{
				{Value: 0, Color: "#94a3b8"},
				{Value: 1, Color: "#22c55e"},
			}),
		buildMetricWidget("partial", "Partial Compliance", 6, 0, 2,
			"partialRepos", "number", []dashboardir.MetricThreshold{
				{Value: 0, Color: "#94a3b8"},
				{Value: 1, Color: "#f59e0b"},
			}),
		buildMetricWidget("non-compliant", "Non-Compliant", 9, 0, 2,
			"nonCompliant", "number", []dashboardir.MetricThreshold{
				{Value: 0, Color: "#94a3b8"},
				{Value: 1, Color: "#ef4444"},
			}),
	)

	// Row 2: Compliance rate + charts
	widgets = append(widgets,
		buildMetricWidget("compliance-rate", "Compliance Rate", 0, 2, 3,
			"complianceRate", "percent", []dashboardir.MetricThreshold{
				{Value: 0, Color: "#ef4444"},
				{Value: 50, Color: "#f59e0b"},
				{Value: 80, Color: "#22c55e"},
			}),
	)

	// Compliance by language bar chart
	widgets = append(widgets, buildLanguageComplianceChart(3, 2, 5, 3))

	// Repos by language pie chart
	widgets = append(widgets, buildLanguageDistributionChart(8, 2, 4, 3))

	// Row 3: Non-compliant repos table
	widgets = append(widgets, buildNonCompliantTable(0, 5, 12, 5))

	// Row 4: All repos table
	widgets = append(widgets, buildAllReposTable(0, 10, 12, 5))

	return widgets
}

// metricWidgetWidth is the standard width for metric widgets in the grid.
const metricWidgetWidth = 3

// metricDataSource is the default data source for metric widgets.
const metricDataSource = "summary"

func buildMetricWidget(id, title string, x, y, h int, valueField, format string, thresholds []dashboardir.MetricThreshold) dashboardir.Widget {
	config := dashboardir.MetricConfig{
		ValueField: valueField,
		Format:     format,
		Thresholds: thresholds,
	}
	if format == "percent" {
		config.FormatOptions = &dashboardir.FormatOptions{Decimals: 1}
	}

	return dashboardir.Widget{
		ID:           id,
		Title:        title,
		Type:         dashboardir.WidgetTypeMetric,
		Position:     dashboardir.Position{X: x, Y: y, W: metricWidgetWidth, H: h},
		DataSourceID: metricDataSource,
		Config:       mustJSON(config),
	}
}

func buildLanguageComplianceChart(x, y, w, h int) dashboardir.Widget {
	chartConfig := map[string]any{
		"marks": []map[string]any{
			{
				"id":       "compliance-bars",
				"geometry": "bar",
				"encode": map[string]string{
					"x": "language",
					"y": "complianceRate",
				},
				"style": map[string]any{
					"color": "#3b82f6",
				},
			},
		},
		"axes": []map[string]any{
			{"id": "x", "type": "category", "position": "bottom"},
			{"id": "y", "type": "value", "position": "left", "name": "Compliance %", "max": 100},
		},
		"tooltip": map[string]any{"show": true, "trigger": "axis"},
		"grid":    map[string]any{"left": "3%", "right": "4%", "bottom": "3%", "containLabel": true},
	}

	return dashboardir.Widget{
		ID:           "language-compliance-chart",
		Title:        "Compliance Rate by Language",
		Type:         dashboardir.WidgetTypeChart,
		Position:     dashboardir.Position{X: x, Y: y, W: w, H: h},
		DataSourceID: "language-stats",
		Config:       mustJSON(chartConfig),
	}
}

func buildLanguageDistributionChart(x, y, w, h int) dashboardir.Widget {
	chartConfig := map[string]any{
		"marks": []map[string]any{
			{
				"id":       "language-pie",
				"geometry": "pie",
				"encode": map[string]string{
					"value": "totalRepos",
					"name":  "language",
				},
			},
		},
		"tooltip": map[string]any{"show": true, "trigger": "item"},
		"legend":  map[string]any{"show": true, "position": "bottom"},
	}

	return dashboardir.Widget{
		ID:           "language-distribution-chart",
		Title:        "Repositories by Language",
		Type:         dashboardir.WidgetTypeChart,
		Position:     dashboardir.Position{X: x, Y: y, W: w, H: h},
		DataSourceID: "language-stats",
		Config:       mustJSON(chartConfig),
	}
}

func buildNonCompliantTable(x, y, w, h int) dashboardir.Widget {
	tableConfig := dashboardir.TableConfig{
		Columns: []dashboardir.TableColumn{
			{Field: "fullName", Header: "Repository", Width: "25%"},
			{Field: "complianceLevel", Header: "Status", Width: "12%"},
			{Field: "languages", Header: "Languages", Width: "18%"},
			{Field: "missing", Header: "Missing Workflows", Width: "30%"},
			{Field: "scanTimeMs", Header: "Scan (ms)", Width: "10%", Align: "right"},
		},
		Sortable: true,
		Striped:  true,
	}

	return dashboardir.Widget{
		ID:           "non-compliant-table",
		Title:        "Repositories Needing Attention",
		Type:         dashboardir.WidgetTypeTable,
		Position:     dashboardir.Position{X: x, Y: y, W: w, H: h},
		DataSourceID: "non-compliant-repos",
		Config:       mustJSON(tableConfig),
	}
}

func buildAllReposTable(x, y, w, h int) dashboardir.Widget {
	tableConfig := dashboardir.TableConfig{
		Columns: []dashboardir.TableColumn{
			{Field: "fullName", Header: "Repository", Width: "30%"},
			{Field: "complianceLevel", Header: "Status", Width: "12%"},
			{Field: "languages", Header: "Languages", Width: "20%"},
			{Field: "compliant", Header: "Compliant", Width: "10%"},
			{Field: "scanTimeMs", Header: "Scan (ms)", Width: "10%", Align: "right"},
		},
		Sortable: true,
		Striped:  true,
		Pagination: &dashboardir.TablePagination{
			Enabled:         true,
			PageSize:        10,
			PageSizeOptions: []int{10, 25, 50},
		},
	}

	return dashboardir.Widget{
		ID:           "all-repos-table",
		Title:        "All Repositories",
		Type:         dashboardir.WidgetTypeTable,
		Position:     dashboardir.Position{X: x, Y: y, W: w, H: h},
		DataSourceID: "repos",
		Config:       mustJSON(tableConfig),
	}
}

func mustJSON(v any) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return data
}
