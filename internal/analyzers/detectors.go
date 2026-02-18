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
	reEnvAssign    = regexp.MustCompile(`^\s*([A-Za-z_][A-Za-z0-9_\-\.]*)\s*=\s*(.+)?$`)
	reYAMLAssign   = regexp.MustCompile(`^\s*([A-Za-z_][A-Za-z0-9_\-\.]*)\s*:\s*(.+)?$`)
	reJSONKey      = regexp.MustCompile(`^\s*"([A-Za-z_][A-Za-z0-9_\-\.]*)"\s*:\s*(.+)?$`)
	rePropsAssign  = regexp.MustCompile(`^\s*([A-Za-z_][A-Za-z0-9_\-\.]*)\s*[:=]\s*(.+)?$`)
	reSecretLike   = regexp.MustCompile(`(?i)(secret|token|password|passwd|api[_\-]?key|private[_\-]?key|client[_\-]?secret|access[_\-]?key|credential)`)

	reTerraformRes = regexp.MustCompile(`^\s*resource\s+"([^"]+)"\s+"([^"]+)"`)
	reK8sKind      = regexp.MustCompile(`^\s*kind\s*:\s*([A-Za-z0-9]+)`)
	reK8sName      = regexp.MustCompile(`^\s*name\s*:\s*([A-Za-z0-9\-_\.]+)`)
	reK8sImage     = regexp.MustCompile(`^\s*image\s*:\s*([A-Za-z0-9/\.\-_:]+)`)
	reDockerFrom   = regexp.MustCompile(`(?i)^from\s+([a-z0-9/\.\-_:]+)`)
	reComposeSvc   = regexp.MustCompile(`^\s{2,}([A-Za-z0-9\-_]+)\s*:\s*$`)
	reComposeImage = regexp.MustCompile(`^\s{4,}image\s*:\s*([A-Za-z0-9/\.\-_:]+)`)
	reGoBuildCmd   = regexp.MustCompile(`\bgo\s+build\b`)
	reDockerBuild  = regexp.MustCompile(`\bdocker\s+build(?:x)?\b`)
	reNpmBuild     = regexp.MustCompile(`\bnpm\s+run\s+build\b`)
	reMavenPkg     = regexp.MustCompile(`\bmvn(?:w)?\s+.*\b(package|install)\b`)
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
	switch file.Ext {
	case ".js", ".jsx", ".ts", ".tsx":
		if detectJSTSInboundEndpointsSemantic(c, file) {
			return
		}
	case ".py":
		if detectPythonInboundEndpointsSemantic(c, file) {
			return
		}
	case ".java":
		if detectJavaInboundEndpointsSemantic(c, file) {
			return
		}
	}
	if file.Ext == ".go" {
		if detectGoInboundEndpointsSemantic(c, file) {
			return
		}
	}
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
	switch file.Ext {
	case ".js", ".jsx", ".ts", ".tsx":
		if detectJSTSOutboundCallsSemantic(c, file) {
			return
		}
	case ".py":
		if detectPythonOutboundCallsSemantic(c, file) {
			return
		}
	case ".java":
		if detectJavaOutboundCallsSemantic(c, file) {
			return
		}
	}
	if file.Ext == ".go" {
		if detectGoOutboundCallsSemantic(c, file) {
			return
		}
	}
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
		recordConfigKey(c, file, m.line, m.col, m.text, m.groups[0], "os.Getenv", "code_ref")
	}
	for _, m := range regexMatchesByLine(file.Lines, reProcessEnv) {
		recordConfigKey(c, file, m.line, m.col, m.text, m.groups[0], "process.env", "code_ref")
	}
	for _, m := range regexMatchesByLine(file.Lines, reProcessEnvIx) {
		recordConfigKey(c, file, m.line, m.col, m.text, m.groups[0], "process.env[]", "code_ref")
	}
	for _, m := range regexMatchesByLine(file.Lines, reSpringValue) {
		recordConfigKey(c, file, m.line, m.col, m.text, m.groups[0], "@Value", "code_ref")
	}
	for _, m := range regexMatchesByLine(file.Lines, rePyGetenv) {
		recordConfigKey(c, file, m.line, m.col, m.text, m.groups[0], "os.getenv", "code_ref")
	}
	for _, m := range regexMatchesByLine(file.Lines, reViperGet) {
		recordConfigKey(c, file, m.line, m.col, m.text, m.groups[0], "viper.Get", "code_ref")
	}

	if isConfigManifestFile(file.Path) {
		for i, line := range file.Lines {
			lineNo := i + 1
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "//") {
				continue
			}
			if m := reEnvAssign.FindStringSubmatch(line); len(m) >= 2 {
				recordConfigKey(c, file, lineNo, 1, line, m[1], "env_assignment", "config_manifest")
				continue
			}
			if m := reJSONKey.FindStringSubmatch(line); len(m) >= 2 {
				recordConfigKey(c, file, lineNo, 1, line, m[1], "json_assignment", "config_manifest")
				continue
			}
			if m := reYAMLAssign.FindStringSubmatch(line); len(m) >= 2 {
				key := m[1]
				lower := strings.ToLower(strings.TrimSpace(key))
				// Skip common structural keys to keep config lineage focused.
				if lower == "apiversion" || lower == "kind" || lower == "metadata" || lower == "spec" {
					continue
				}
				recordConfigKey(c, file, lineNo, 1, line, key, "yaml_assignment", "config_manifest")
				continue
			}
			if m := rePropsAssign.FindStringSubmatch(line); len(m) >= 2 {
				recordConfigKey(c, file, lineNo, 1, line, m[1], "properties_assignment", "config_manifest")
			}
		}
	}
}

func detectQueueAndDBCalls(c *collector, file sourceFile) {
	if file.Ext == ".go" {
		if detectGoQueueAndDBCallsSemantic(c, file) {
			return
		}
	}
	if file.Ext == ".py" {
		if detectPythonQueueAndDBCallsSemantic(c, file) {
			return
		}
	}
	switch file.Ext {
	case ".js", ".jsx", ".ts", ".tsx":
		if detectJSTSQueueAndDBCallsSemantic(c, file) {
			return
		}
	}
	if file.Ext == ".java" {
		if detectJavaQueueAndDBCallsSemantic(c, file) {
			return
		}
	}
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
			runCmd := strings.TrimSpace(m.groups[0])
			c.addFactWithEvidence("PipelineStep", map[string]any{"provider": "github-actions", "kind": "run", "value": runCmd}, file, m.line, m.col, m.text, func() { c.report.PipelineSteps++ })
			detectBuildArtifactsFromCommand(c, file, m.line, m.col, m.text, runCmd, "github-actions")
		}
		for _, m := range regexMatchesByLine(file.Lines, reGHSecret) {
			c.addFactWithEvidence("PipelineStep", map[string]any{"provider": "github-actions", "kind": "secret", "value": strings.TrimSpace(m.text)}, file, m.line, m.col, m.text, func() { c.report.PipelineSteps++ })
			c.addFactWithEvidence("SensitiveSurface", map[string]any{
				"kind":           "pipeline_secret",
				"reference":      strings.TrimSpace(m.text),
				"classification": "secret-like",
				"source_kind":    "ci",
				"environment":    inferEnvironmentScope(file.Path, ""),
			}, file, m.line, m.col, m.text, func() { c.report.SensitiveSurfaces++ })
		}
	}

	if base == ".gitlab-ci.yml" || base == "jenkinsfile" {
		c.addFactWithEvidence("PipelineStep", map[string]any{"provider": base, "kind": "pipeline-file", "value": file.Path}, file, 1, 1, file.Path, func() { c.report.PipelineSteps++ })
		for i, line := range file.Lines {
			detectBuildArtifactsFromCommand(c, file, i+1, 1, line, line, base)
		}
	}

	if strings.HasSuffix(lower, ".tf") {
		for _, m := range regexMatchesByLine(file.Lines, reTerraformRes) {
			c.addFactWithEvidence("InfraResource", map[string]any{"provider": "terraform", "resource_type": m.groups[0], "name": m.groups[1]}, file, m.line, m.col, m.text, func() { c.report.InfraResources++ })
			c.addFactWithEvidence("Deployment", map[string]any{
				"platform":      "terraform",
				"resource_type": m.groups[0],
				"name":          m.groups[1],
				"file":          file.Path,
			}, file, m.line, m.col, m.text, func() { c.report.Deployments++ })
		}
	}
	if strings.Contains(lower, "helm/") && base == "chart.yaml" {
		c.addFactWithEvidence("InfraResource", map[string]any{"provider": "helm", "kind": "chart", "file": file.Path}, file, 1, 1, "Chart.yaml", func() { c.report.InfraResources++ })
		c.addFactWithEvidence("Deployment", map[string]any{
			"platform": "helm",
			"name":     filepath.Base(filepath.Dir(file.Path)),
			"file":     file.Path,
		}, file, 1, 1, "Chart.yaml", func() { c.report.Deployments++ })
	}
	if strings.HasPrefix(lower, "k8s/") || base == "kustomization.yaml" || base == "kustomization.yml" {
		lastKind := ""
		lastName := ""
		for _, m := range regexMatchesByLine(file.Lines, reK8sKind) {
			lastKind = strings.TrimSpace(m.groups[0])
			c.addFactWithEvidence("InfraResource", map[string]any{"provider": "kubernetes", "kind": lastKind, "file": file.Path}, file, m.line, m.col, m.text, func() { c.report.InfraResources++ })
			if strings.EqualFold(lastKind, "Deployment") || strings.EqualFold(lastKind, "StatefulSet") || strings.EqualFold(lastKind, "DaemonSet") {
				c.addFactWithEvidence("Deployment", map[string]any{
					"platform":      "kubernetes",
					"resource_kind": lastKind,
					"name":          lastName,
					"file":          file.Path,
				}, file, m.line, m.col, m.text, func() { c.report.Deployments++ })
			}
		}
		for _, m := range regexMatchesByLine(file.Lines, reK8sName) {
			lastName = strings.TrimSpace(m.groups[0])
		}
		for _, m := range regexMatchesByLine(file.Lines, reK8sImage) {
			image := strings.TrimSpace(m.groups[0])
			if image == "" {
				continue
			}
			c.addFactWithEvidence("BuildArtifact", map[string]any{
				"artifact_type": "container-image",
				"name":          image,
				"produced_by":   "kubernetes-manifest",
				"file":          file.Path,
			}, file, m.line, m.col, m.text, func() { c.report.BuildArtifacts++ })
		}
	}

	if base == "dockerfile" {
		for _, m := range regexMatchesByLine(file.Lines, reDockerFrom) {
			image := strings.TrimSpace(m.groups[0])
			if image == "" {
				continue
			}
			c.addFactWithEvidence("BuildArtifact", map[string]any{
				"artifact_type": "container-image",
				"name":          image,
				"produced_by":   "dockerfile",
				"file":          file.Path,
			}, file, m.line, m.col, m.text, func() { c.report.BuildArtifacts++ })
		}
	}

	if base == "docker-compose.yml" || base == "docker-compose.yaml" || base == "compose.yml" || base == "compose.yaml" {
		currentService := ""
		for i, line := range file.Lines {
			if m := reComposeSvc.FindStringSubmatch(line); len(m) >= 2 {
				currentService = strings.TrimSpace(m[1])
			}
			if m := reComposeImage.FindStringSubmatch(line); len(m) >= 2 {
				image := strings.TrimSpace(m[1])
				if image == "" {
					continue
				}
				c.addFactWithEvidence("BuildArtifact", map[string]any{
					"artifact_type": "container-image",
					"name":          image,
					"service":       currentService,
					"produced_by":   "docker-compose",
					"file":          file.Path,
				}, file, i+1, 1, line, func() { c.report.BuildArtifacts++ })
				c.addFactWithEvidence("Deployment", map[string]any{
					"platform": "docker-compose",
					"name":     currentService,
					"image":    image,
					"file":     file.Path,
				}, file, i+1, 1, line, func() { c.report.Deployments++ })
			}
		}
	}
}

func detectBuildArtifactsFromCommand(c *collector, file sourceFile, line int, col int, snippet string, cmd string, provider string) {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return
	}
	switch {
	case reDockerBuild.MatchString(cmd):
		c.addFactWithEvidence("BuildArtifact", map[string]any{
			"artifact_type": "container-image",
			"name":          "docker-image",
			"build_command": cmd,
			"provider":      provider,
			"file":          file.Path,
		}, file, line, col, snippet, func() { c.report.BuildArtifacts++ })
	case reGoBuildCmd.MatchString(cmd):
		c.addFactWithEvidence("BuildArtifact", map[string]any{
			"artifact_type": "binary",
			"name":          "go-binary",
			"build_command": cmd,
			"provider":      provider,
			"file":          file.Path,
		}, file, line, col, snippet, func() { c.report.BuildArtifacts++ })
	case reNpmBuild.MatchString(cmd):
		c.addFactWithEvidence("BuildArtifact", map[string]any{
			"artifact_type": "web-bundle",
			"name":          "npm-build-output",
			"build_command": cmd,
			"provider":      provider,
			"file":          file.Path,
		}, file, line, col, snippet, func() { c.report.BuildArtifacts++ })
	case reMavenPkg.MatchString(cmd):
		c.addFactWithEvidence("BuildArtifact", map[string]any{
			"artifact_type": "jar",
			"name":          "maven-artifact",
			"build_command": cmd,
			"provider":      provider,
			"file":          file.Path,
		}, file, line, col, snippet, func() { c.report.BuildArtifacts++ })
	}
}

func recordConfigKey(c *collector, file sourceFile, line int, col int, snippet string, key string, pattern string, sourceKind string) {
	key = strings.TrimSpace(key)
	if key == "" {
		return
	}
	env := inferEnvironmentScope(file.Path, key)
	sensitive := reSecretLike.MatchString(strings.ToLower(key))
	c.addFactWithEvidence("ConfigKey", map[string]any{
		"key":         key,
		"pattern":     pattern,
		"source_kind": sourceKind,
		"environment": env,
		"sensitive":   sensitive,
		"file":        file.Path,
	}, file, line, col, snippet, func() { c.report.ConfigKeys++ })
	if sensitive {
		c.addFactWithEvidence("SensitiveSurface", map[string]any{
			"kind":           "config_key",
			"key":            key,
			"classification": "secret-like",
			"source_kind":    sourceKind,
			"environment":    env,
			"file":           file.Path,
		}, file, line, col, snippet, func() { c.report.SensitiveSurfaces++ })
	}
}

func inferEnvironmentScope(path string, key string) string {
	p := strings.ToLower(path)
	k := strings.ToLower(key)
	switch {
	case strings.Contains(p, "prod"), strings.Contains(k, "prod_"), strings.Contains(k, ".prod"):
		return "prod"
	case strings.Contains(p, "stag"), strings.Contains(k, "stage_"), strings.Contains(k, "staging_"):
		return "staging"
	case strings.Contains(p, "dev"), strings.Contains(k, "dev_"), strings.Contains(k, ".dev"):
		return "dev"
	case strings.Contains(p, "test"), strings.Contains(p, "qa"), strings.Contains(k, "test_"), strings.Contains(k, "qa_"):
		return "test"
	default:
		return "default"
	}
}

func isConfigManifestFile(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	lower := strings.ToLower(path)
	if strings.HasPrefix(base, ".env") {
		return true
	}
	if strings.Contains(lower, "/config/") || strings.Contains(lower, "\\config\\") {
		return true
	}
	if strings.HasPrefix(base, "application.") || strings.HasPrefix(base, "settings.") {
		return true
	}
	switch filepath.Ext(base) {
	case ".yaml", ".yml", ".json", ".toml", ".ini", ".properties", ".conf":
		return strings.Contains(lower, "config")
	default:
		return false
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
