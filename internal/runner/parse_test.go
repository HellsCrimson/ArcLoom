package runner

import "testing"

func TestParseTflint(t *testing.T) {
	data := []byte(`{
		"issues": [
			{"rule": {"name": "terraform_deprecated_index", "severity": "WARNING"},
			 "message": "deprecated index syntax",
			 "range": {"filename": "main.tf", "start": {"line": 12}}},
			{"rule": {"name": "aws_instance_invalid_type", "severity": "ERROR"},
			 "message": "invalid instance type",
			 "range": {"filename": "ec2.tf", "start": {"line": 3}}}
		],
		"errors": [
			{"message": "failed to load config", "severity": "error"}
		]
	}`)
	res, err := parseTflint(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Issues) != 3 {
		t.Fatalf("expected 3 issues (2 + 1 error), got %d", len(res.Issues))
	}
	if res.WarningCount != 1 || res.ErrorCount != 2 {
		t.Fatalf("counts: warn=%d err=%d, want 1/2", res.WarningCount, res.ErrorCount)
	}
	if res.Issues[0].Severity != "warning" || res.Issues[0].Filename != "main.tf" || res.Issues[0].Line != 12 {
		t.Fatalf("first issue not parsed: %+v", res.Issues[0])
	}
}

func TestParseTflintEmpty(t *testing.T) {
	res, err := parseTflint([]byte("   "))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Issues) != 0 || res.ErrorCount != 0 || res.WarningCount != 0 {
		t.Fatalf("empty output should yield no issues: %+v", res)
	}
}

func TestParseInfracost(t *testing.T) {
	data := []byte(`{
		"projects": [
			{"breakdown": {"resources": [
				{"name": "aws_instance.web", "monthlyCost": "140.16"},
				{"name": "aws_s3_bucket.logs", "monthlyCost": null},
				{"name": "aws_db_instance.main", "monthlyCost": "220.00"}
			]}}
		],
		"totalMonthlyCost": "360.16"
	}`)
	prices, err := parseInfracost(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(prices) != 2 {
		t.Fatalf("expected 2 priced resources (null skipped), got %d: %v", len(prices), prices)
	}
	if prices["aws_instance.web"] != 140.16 || prices["aws_db_instance.main"] != 220 {
		t.Fatalf("prices not parsed: %v", prices)
	}
	if _, ok := prices["aws_s3_bucket.logs"]; ok {
		t.Fatal("resource with null monthlyCost should be skipped")
	}
}
