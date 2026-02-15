package analyzers

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	reGoMain           = regexp.MustCompile(`func\s+main\s*\(`)
	rePyMain           = regexp.MustCompile(`if\s+__name__\s*==\s*["']__main__["']\s*:`)
	reJavaMain         = regexp.MustCompile(`public\s+static\s+void\s+main\s*\(`)
	reDockerEntrypoint = regexp.MustCompile(`(?i)^(entrypoint|cmd)\s+(.+)$`)
	reDockerExpose     = regexp.MustCompile(`(?i)^expose\s+([0-9]+)`)

	reExpressRoute = regexp.MustCompile(`\b(?:app|router)\.(get|post|put|patch|delete)\(\s*["']([^"']+)["']`)
	reGoRoute      = regexp.MustCompile(`\b[A-Za-z_][A-Za-z0-9_]*\.(GET|POST|PUT|PATCH|DELETE)\(\s*["']([^"']+)["']`)
	reSpringRoute  = regexp.MustCompile(`@(GetMapping|PostMapping|PutMapping|DeleteMapping|PatchMapping|RequestMapping)\(([^)]*)\)`)

	reGoHttpNewReq = regexp.MustCompile(`http\.NewRequest\(\s*"([A-Z]+)"\s*,\s*([^,\)]+)`)
	reGoHttpSimple = regexp.MustCompile(`http\.(Get|Post)\(\s*([^\)]+)\)`)
	reGoClientDo   = regexp.MustCompile(`\.[dD]o\(\s*req\s*\)`)
	reFetch        = regexp.MustCompile(`\bfetch\(\s*([^,\)]+)`)
	reAxios        = regexp.MustCompile(`\baxios\.(get|post|put|patch|delete)\(\s*["']([^"']+)["']`)
	reRestTemplate = regexp.MustCompile(`\brestTemplate\.(getForObject|postForObject|exchange)\(`)

	reKafkaTopicAttr = regexp.MustCompile(`\bTopic\s*:\s*["']([^"']+)["']`)
	reKafkaWrite     = regexp.MustCompile(`\bWriteMessages\(`)
	reKafkaRead      = regexp.MustCompile(`\bReadMessage\(`)
	reSaramaSend     = regexp.MustCompile(`\bSendMessage\(`)
	reSaramaConsume  = regexp.MustCompile(`\bConsume(?:Partition|Claim)?\(`)
	reAMQPPublish    = regexp.MustCompile(`\bPublish\(\s*["']([^"']*)["']\s*,\s*["']([^"']*)["']`)
	reAMQPConsume    = regexp.MustCompile(`\bConsume\(\s*["']([^"']+)["']`)
	reSQSSend        = regexp.MustCompile(`\b(?:sqsClient|sqs)\.SendMessage\(`)
	reSQSReceive     = regexp.MustCompile(`\b(?:sqsClient|sqs)\.ReceiveMessage\(`)
	reSNSPublish     = regexp.MustCompile(`\b(?:snsClient|sns)\.Publish\(`)
	reAWSQueueURL    = regexp.MustCompile(`\bQueueUrl\s*:\s*["']([^"']+)["']`)
	reAWSTopicARN    = regexp.MustCompile(`\bTopicArn\s*:\s*["']([^"']+)["']`)

	reSQLOpen      = regexp.MustCompile(`\bsql\.Open\(\s*["']([^"']+)["']`)
	rePGXConnect   = regexp.MustCompile(`\bpgx(?:pool)?\.Connect(?:Config)?\(`)
	reGormOpen     = regexp.MustCompile(`\bgorm\.Open\(`)
	reDBQueryRead  = regexp.MustCompile(`\.(Query|QueryRow|Select|Find|Get)\(`)
	reDBQueryWrite = regexp.MustCompile(`\.(Exec|Create|Update|Delete|Insert)\(`)

	reGetenvGo     = regexp.MustCompile(`os\.Getenv\(\s*"([A-Za-z0-9_\-\.]+)"\s*\)`)
	reProcessEnv   = regexp.MustCompile(`process\.env\.([A-Za-z0-9_]+)`)
	reProcessEnvIx = regexp.MustCompile(`process\.env\[\s*["']([A-Za-z0-9_\-\.]+)["']\s*\]`)
	reSpringValue  = regexp.MustCompile(`@Value\(\s*"\$\{([^}]+)\}"\s*\)`)
	rePyGetenv     = regexp.MustCompile(`os\.getenv\(\s*["']([A-Za-z0-9_\-\.]+)["']`)
	reViperGet     = regexp.MustCompile(`viper\.Get(?:String|Int|Bool|Duration|Float64)?\(\s*"([A-Za-z0-9_\-\.]+)"\s*\)`)

	reTerraformRes = regexp.MustCompile(`^\s*resource\s+"([^"]+)"\s+"([^"]+)"`)
	reK8sKind      = regexp.MustCompile(`^\s*kind\s*:\s*([A-Za-z0-9]+)`)
	reGHUses       = regexp.MustCompile(`^\s*-?\s*uses\s*:\s*(.+)$`)
	reGHRun        = regexp.MustCompile(`^\s*-?\s*run\s*:\s*(.+)$`)
	reGHSecret     = regexp.MustCompile(`secrets\.[A-Za-z0-9_]+`)
)

func detectRuntimeUnits(c *collector, file sourceFile) {
	if file.Ext == ".go" && strings.Contains(file.Text, "package main") {
		for _, m := range regexMatchesByLine(file.Lines, reGoMain) {
			c.addFactWithEvidence("RuntimeUnit", map[string]any{"language": "go", "kind": "main", "file": file.Path}, file, m.line, m.col, m.text, func() { c.report.RuntimeUnits++ })
		}
	}
	if file.Ext == ".py" {
		for _, m := range regexMatchesByLine(file.Lines, rePyMain) {
			c.addFactWithEvidence("RuntimeUnit", map[string]any{"language": "python", "kind": "__main__", "file": file.Path}, file, m.line, m.col, m.text, func() { c.report.RuntimeUnits++ })
		}
	}
	if file.Ext == ".java" {
		for _, m := range regexMatchesByLine(file.Lines, reJavaMain) {
			c.addFactWithEvidence("RuntimeUnit", map[string]any{"language": "java", "kind": "main", "file": file.Path}, file, m.line, m.col, m.text, func() { c.report.RuntimeUnits++ })
		}
	}

	if strings.EqualFold(filepath.Base(file.Path), "package.json") {
		var doc map[string]any
		if err := json.Unmarshal([]byte(file.Text), &doc); err == nil {
			if scripts, ok := doc["scripts"].(map[string]any); ok {
				for _, key := range []string{"start", "dev", "serve"} {
					if cmd, exists := scripts[key]; exists {
						c.addFactWithEvidence("RuntimeUnit", map[string]any{"language": "node", "kind": "script", "script": key, "command": cmd}, file, 1, 1, "scripts", func() { c.report.RuntimeUnits++ })
					}
				}
			}
		}
	}

	if strings.EqualFold(filepath.Base(file.Path), "dockerfile") {
		for _, m := range regexMatchesByLine(file.Lines, reDockerEntrypoint) {
			c.addFactWithEvidence("RuntimeUnit", map[string]any{"language": "container", "kind": strings.ToUpper(m.groups[0]), "value": strings.TrimSpace(m.groups[1])}, file, m.line, m.col, m.text, func() { c.report.RuntimeUnits++ })
		}
		for _, m := range regexMatchesByLine(file.Lines, reDockerExpose) {
			port := ""
			if len(m.groups) > 0 {
				port = m.groups[0]
			}
			c.addFactWithEvidence("RuntimeUnit", map[string]any{"language": "container", "kind": "EXPOSE", "port": port}, file, m.line, m.col, m.text, func() { c.report.RuntimeUnits++ })
		}
	}
}

func detectInboundEndpoints(c *collector, file sourceFile) {
	for _, m := range regexMatchesByLine(file.Lines, reExpressRoute) {
		c.addFactWithEvidence("Endpoint", map[string]any{"direction": "inbound", "method": strings.ToUpper(m.groups[0]), "path": m.groups[1], "framework": "express-like"}, file, m.line, m.col, m.text, func() { c.report.Endpoints++ })
	}
	for _, m := range regexMatchesByLine(file.Lines, reGoRoute) {
		c.addFactWithEvidence("Endpoint", map[string]any{"direction": "inbound", "method": strings.ToUpper(m.groups[0]), "path": m.groups[1], "framework": "go-router"}, file, m.line, m.col, m.text, func() { c.report.Endpoints++ })
	}
	for _, m := range regexMatchesByLine(file.Lines, reSpringRoute) {
		path := m.groups[1]
		if extracted := extractQuoted(path); extracted != "" {
			path = extracted
		}
		method := strings.TrimSuffix(strings.TrimPrefix(m.groups[0], "Request"), "Mapping")
		if strings.EqualFold(method, "") || strings.EqualFold(m.groups[0], "RequestMapping") {
			method = "ANY"
		}
		c.addFactWithEvidence("Endpoint", map[string]any{"direction": "inbound", "method": strings.ToUpper(method), "path": path, "framework": "spring"}, file, m.line, m.col, m.text, func() { c.report.Endpoints++ })
	}
}

func detectOutboundCalls(c *collector, file sourceFile) {
	for _, m := range regexMatchesByLine(file.Lines, reGoHttpNewReq) {
		c.addFactWithEvidence("ExternalCall", map[string]any{"protocol": "http", "method": m.groups[0], "target": strings.TrimSpace(m.groups[1]), "library": "go-net-http"}, file, m.line, m.col, m.text, func() { c.report.ExternalCalls++ })
	}
	for _, m := range regexMatchesByLine(file.Lines, reGoHttpSimple) {
		method := strings.ToUpper(m.groups[0])
		if method == "GET" || method == "POST" {
			c.addFactWithEvidence("ExternalCall", map[string]any{"protocol": "http", "method": method, "target": strings.TrimSpace(m.groups[1]), "library": "go-net-http"}, file, m.line, m.col, m.text, func() { c.report.ExternalCalls++ })
		}
	}
	for _, m := range regexMatchesByLine(file.Lines, reGoClientDo) {
		c.addFactWithEvidence("ExternalCall", map[string]any{"protocol": "http", "method": "UNKNOWN", "target": "request-object", "library": "go-net-http"}, file, m.line, m.col, m.text, func() { c.report.ExternalCalls++ })
	}
	for _, m := range regexMatchesByLine(file.Lines, reFetch) {
		c.addFactWithEvidence("ExternalCall", map[string]any{"protocol": "http", "method": "UNKNOWN", "target": strings.TrimSpace(m.groups[0]), "library": "fetch"}, file, m.line, m.col, m.text, func() { c.report.ExternalCalls++ })
	}
	for _, m := range regexMatchesByLine(file.Lines, reAxios) {
		c.addFactWithEvidence("ExternalCall", map[string]any{"protocol": "http", "method": strings.ToUpper(m.groups[0]), "target": m.groups[1], "library": "axios"}, file, m.line, m.col, m.text, func() { c.report.ExternalCalls++ })
	}
	for _, m := range regexMatchesByLine(file.Lines, reRestTemplate) {
		c.addFactWithEvidence("ExternalCall", map[string]any{"protocol": "http", "method": strings.ToUpper(m.groups[0]), "target": "java-expression", "library": "resttemplate"}, file, m.line, m.col, m.text, func() { c.report.ExternalCalls++ })
	}
}

func detectConfigKeys(c *collector, file sourceFile) {
	for _, m := range regexMatchesByLine(file.Lines, reGetenvGo) {
		c.addFactWithEvidence("ConfigKey", map[string]any{"key": m.groups[0], "pattern": "os.Getenv"}, file, m.line, m.col, m.text, func() { c.report.ConfigKeys++ })
	}
	for _, m := range regexMatchesByLine(file.Lines, reProcessEnv) {
		c.addFactWithEvidence("ConfigKey", map[string]any{"key": m.groups[0], "pattern": "process.env"}, file, m.line, m.col, m.text, func() { c.report.ConfigKeys++ })
	}
	for _, m := range regexMatchesByLine(file.Lines, reProcessEnvIx) {
		c.addFactWithEvidence("ConfigKey", map[string]any{"key": m.groups[0], "pattern": "process.env[]"}, file, m.line, m.col, m.text, func() { c.report.ConfigKeys++ })
	}
	for _, m := range regexMatchesByLine(file.Lines, reSpringValue) {
		c.addFactWithEvidence("ConfigKey", map[string]any{"key": m.groups[0], "pattern": "@Value"}, file, m.line, m.col, m.text, func() { c.report.ConfigKeys++ })
	}
	for _, m := range regexMatchesByLine(file.Lines, rePyGetenv) {
		c.addFactWithEvidence("ConfigKey", map[string]any{"key": m.groups[0], "pattern": "os.getenv"}, file, m.line, m.col, m.text, func() { c.report.ConfigKeys++ })
	}
	for _, m := range regexMatchesByLine(file.Lines, reViperGet) {
		c.addFactWithEvidence("ConfigKey", map[string]any{"key": m.groups[0], "pattern": "viper.Get"}, file, m.line, m.col, m.text, func() { c.report.ConfigKeys++ })
	}
}

func detectQueueAndDBCalls(c *collector, file sourceFile) {
	for _, m := range regexMatchesByLine(file.Lines, reKafkaWrite) {
		topic := nearestGroupOnLine(file.Lines, m.line, reKafkaTopicAttr)
		if topic == "" {
			topic = "kafka:unknown-topic"
		}
		c.addFactWithEvidence("ExternalCall", map[string]any{"protocol": "queue", "method": "PUBLISH", "target": topic, "library": "kafka-go", "queue_operation": "publish", "queue_kind": "kafka"}, file, m.line, m.col, m.text, func() { c.report.ExternalCalls++ })
	}
	for _, m := range regexMatchesByLine(file.Lines, reKafkaRead) {
		topic := nearestGroupOnLine(file.Lines, m.line, reKafkaTopicAttr)
		if topic == "" {
			topic = "kafka:unknown-topic"
		}
		c.addFactWithEvidence("ExternalCall", map[string]any{"protocol": "queue", "method": "CONSUME", "target": topic, "library": "kafka-go", "queue_operation": "consume", "queue_kind": "kafka"}, file, m.line, m.col, m.text, func() { c.report.ExternalCalls++ })
	}
	for _, m := range regexMatchesByLine(file.Lines, reSaramaSend) {
		topic := nearestGroupOnLine(file.Lines, m.line, reKafkaTopicAttr)
		if topic == "" {
			topic = "kafka:unknown-topic"
		}
		c.addFactWithEvidence("ExternalCall", map[string]any{"protocol": "queue", "method": "PUBLISH", "target": topic, "library": "sarama", "queue_operation": "publish", "queue_kind": "kafka"}, file, m.line, m.col, m.text, func() { c.report.ExternalCalls++ })
	}
	for _, m := range regexMatchesByLine(file.Lines, reSaramaConsume) {
		topic := nearestGroupOnLine(file.Lines, m.line, reKafkaTopicAttr)
		if topic == "" {
			topic = "kafka:unknown-topic"
		}
		c.addFactWithEvidence("ExternalCall", map[string]any{"protocol": "queue", "method": "CONSUME", "target": topic, "library": "sarama", "queue_operation": "consume", "queue_kind": "kafka"}, file, m.line, m.col, m.text, func() { c.report.ExternalCalls++ })
	}
	for _, m := range regexMatchesByLine(file.Lines, reAMQPPublish) {
		target := strings.TrimSpace(m.groups[1])
		if target == "" {
			target = strings.TrimSpace(m.groups[0])
		}
		if target == "" {
			target = "rabbitmq:unknown"
		}
		c.addFactWithEvidence("ExternalCall", map[string]any{"protocol": "queue", "method": "PUBLISH", "target": target, "library": "amqp", "queue_operation": "publish", "queue_kind": "rabbitmq"}, file, m.line, m.col, m.text, func() { c.report.ExternalCalls++ })
	}
	for _, m := range regexMatchesByLine(file.Lines, reAMQPConsume) {
		target := strings.TrimSpace(m.groups[0])
		if target == "" {
			target = "rabbitmq:unknown"
		}
		c.addFactWithEvidence("ExternalCall", map[string]any{"protocol": "queue", "method": "CONSUME", "target": target, "library": "amqp", "queue_operation": "consume", "queue_kind": "rabbitmq"}, file, m.line, m.col, m.text, func() { c.report.ExternalCalls++ })
	}
	for _, m := range regexMatchesByLine(file.Lines, reSQSSend) {
		target := nearestGroupOnLine(file.Lines, m.line, reAWSQueueURL)
		if target == "" {
			target = "sqs:unknown-queue"
		}
		c.addFactWithEvidence("ExternalCall", map[string]any{"protocol": "queue", "method": "PUBLISH", "target": target, "library": "aws-sdk-sqs", "queue_operation": "publish", "queue_kind": "sqs"}, file, m.line, m.col, m.text, func() { c.report.ExternalCalls++ })
	}
	for _, m := range regexMatchesByLine(file.Lines, reSQSReceive) {
		target := nearestGroupOnLine(file.Lines, m.line, reAWSQueueURL)
		if target == "" {
			target = "sqs:unknown-queue"
		}
		c.addFactWithEvidence("ExternalCall", map[string]any{"protocol": "queue", "method": "CONSUME", "target": target, "library": "aws-sdk-sqs", "queue_operation": "consume", "queue_kind": "sqs"}, file, m.line, m.col, m.text, func() { c.report.ExternalCalls++ })
	}
	for _, m := range regexMatchesByLine(file.Lines, reSNSPublish) {
		target := nearestGroupOnLine(file.Lines, m.line, reAWSTopicARN)
		if target == "" {
			target = "sns:unknown-topic"
		}
		c.addFactWithEvidence("ExternalCall", map[string]any{"protocol": "queue", "method": "PUBLISH", "target": target, "library": "aws-sdk-sns", "queue_operation": "publish", "queue_kind": "sns"}, file, m.line, m.col, m.text, func() { c.report.ExternalCalls++ })
	}

	for _, m := range regexMatchesByLine(file.Lines, reSQLOpen) {
		target := strings.TrimSpace(m.groups[0])
		if target == "" {
			target = "db:unknown"
		}
		c.addFactWithEvidence("ExternalCall", map[string]any{"protocol": "db", "method": "CONNECT", "target": target, "library": "database/sql", "db_operation": "connect"}, file, m.line, m.col, m.text, func() { c.report.ExternalCalls++ })
	}
	for _, m := range regexMatchesByLine(file.Lines, rePGXConnect) {
		c.addFactWithEvidence("ExternalCall", map[string]any{"protocol": "db", "method": "CONNECT", "target": "postgres", "library": "pgx", "db_operation": "connect"}, file, m.line, m.col, m.text, func() { c.report.ExternalCalls++ })
	}
	for _, m := range regexMatchesByLine(file.Lines, reGormOpen) {
		c.addFactWithEvidence("ExternalCall", map[string]any{"protocol": "db", "method": "CONNECT", "target": "gorm", "library": "gorm", "db_operation": "connect"}, file, m.line, m.col, m.text, func() { c.report.ExternalCalls++ })
	}
	for _, m := range regexMatchesByLine(file.Lines, reDBQueryRead) {
		c.addFactWithEvidence("ExternalCall", map[string]any{"protocol": "db", "method": "READ", "target": "db", "library": "db-client", "db_operation": "read"}, file, m.line, m.col, m.text, func() { c.report.ExternalCalls++ })
	}
	for _, m := range regexMatchesByLine(file.Lines, reDBQueryWrite) {
		c.addFactWithEvidence("ExternalCall", map[string]any{"protocol": "db", "method": "WRITE", "target": "db", "library": "db-client", "db_operation": "write"}, file, m.line, m.col, m.text, func() { c.report.ExternalCalls++ })
	}
}

func nearestGroupOnLine(lines []string, line int, expr *regexp.Regexp) string {
	if line < 1 || line > len(lines) {
		return ""
	}
	for i := max(1, line-2); i <= min(len(lines), line+2); i++ {
		matches := regexMatchesByLine([]string{lines[i-1]}, expr)
		if len(matches) == 0 || len(matches[0].groups) == 0 {
			continue
		}
		v := strings.TrimSpace(matches[0].groups[0])
		if v != "" {
			return v
		}
	}
	return ""
}

func min(a int, b int) int {
	if a < b {
		return a
	}
	return b
}

func detectCIIaC(c *collector, file sourceFile) {
	lower := strings.ToLower(file.Path)
	base := strings.ToLower(filepath.Base(file.Path))

	if strings.HasPrefix(lower, ".github/workflows/") {
		for _, m := range regexMatchesByLine(file.Lines, reGHUses) {
			c.addFactWithEvidence("PipelineStep", map[string]any{"provider": "github-actions", "kind": "uses", "value": strings.TrimSpace(m.groups[0])}, file, m.line, m.col, m.text, func() { c.report.PipelineSteps++ })
		}
		for _, m := range regexMatchesByLine(file.Lines, reGHRun) {
			c.addFactWithEvidence("PipelineStep", map[string]any{"provider": "github-actions", "kind": "run", "value": strings.TrimSpace(m.groups[0])}, file, m.line, m.col, m.text, func() { c.report.PipelineSteps++ })
		}
		for _, m := range regexMatchesByLine(file.Lines, reGHSecret) {
			c.addFactWithEvidence("PipelineStep", map[string]any{"provider": "github-actions", "kind": "secret", "value": strings.TrimSpace(m.text)}, file, m.line, m.col, m.text, func() { c.report.PipelineSteps++ })
		}
	}

	if base == ".gitlab-ci.yml" || base == "jenkinsfile" {
		c.addFactWithEvidence("PipelineStep", map[string]any{"provider": base, "kind": "pipeline-file", "value": file.Path}, file, 1, 1, file.Path, func() { c.report.PipelineSteps++ })
	}

	if strings.HasSuffix(lower, ".tf") {
		for _, m := range regexMatchesByLine(file.Lines, reTerraformRes) {
			c.addFactWithEvidence("InfraResource", map[string]any{"provider": "terraform", "resource_type": m.groups[0], "name": m.groups[1]}, file, m.line, m.col, m.text, func() { c.report.InfraResources++ })
		}
	}
	if strings.Contains(lower, "helm/") && base == "chart.yaml" {
		c.addFactWithEvidence("InfraResource", map[string]any{"provider": "helm", "kind": "chart", "file": file.Path}, file, 1, 1, "Chart.yaml", func() { c.report.InfraResources++ })
	}
	if strings.HasPrefix(lower, "k8s/") || base == "kustomization.yaml" || base == "kustomization.yml" {
		for _, m := range regexMatchesByLine(file.Lines, reK8sKind) {
			c.addFactWithEvidence("InfraResource", map[string]any{"provider": "kubernetes", "kind": m.groups[0], "file": file.Path}, file, m.line, m.col, m.text, func() { c.report.InfraResources++ })
		}
	}
}

func extractQuoted(value string) string {
	for _, quote := range []string{"\"", "'"} {
		start := strings.Index(value, quote)
		if start == -1 {
			continue
		}
		end := strings.Index(value[start+1:], quote)
		if end == -1 {
			continue
		}
		return value[start+1 : start+1+end]
	}
	return ""
}
