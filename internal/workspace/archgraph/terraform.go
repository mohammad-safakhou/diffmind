package archgraph

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Terraform is where SNS→SQS fan-out actually lives: producers publish to a
// topic, each consumer service gets its own queue, and the link between them
// is an aws_sns_topic_subscription resource in an infra repo — invisible to
// per-service code analysis. Scanning these subscriptions is what lets the
// graph connect publisher → topic → queue → consumer.

// QueueSubscription is one SNS topic → SQS queue fan-out link.
type QueueSubscription struct {
	Topic  string // SNS topic name
	Queue  string // subscribed SQS queue name
	Source string // repo-relative .tf file the subscription was found in
}

var (
	tfResourceRe = regexp.MustCompile(`(?ms)^resource\s+"(aws_sqs_queue|aws_sns_topic|aws_sns_topic_subscription)"\s+"([^"]+)"\s*\{(.*?)^\}`)
	tfNameAttrRe = regexp.MustCompile(`(?m)^\s*name\s*=\s*"([^"]+)"`)
	tfTopicArnRe = regexp.MustCompile(`(?m)^\s*topic_arn\s*=\s*(.+?)\s*$`)
	tfEndpointRe = regexp.MustCompile(`(?m)^\s*endpoint\s*=\s*(.+?)\s*$`)
	tfProtocolRe = regexp.MustCompile(`(?m)^\s*protocol\s*=\s*"([^"]+)"`)
	// var.aws_sns_topic-catalogue_campaign_sns-arn → catalogue_campaign_sns
	tfTopicVarRe = regexp.MustCompile(`\bvar\.aws_sns_topic[-_](.+?)[-_]arn\b`)
)

// ScanTerraformSubscriptions extracts SNS→SQS subscription links from all .tf
// files under repoPath. Resolution is deterministic: resource references
// (aws_sns_topic.<res>.arn / aws_sqs_queue.<res>.arn) resolve against name
// attributes collected across the repo's .tf files; literal ARNs use their
// trailing segment; topic variables use the aws_sns_topic-<name>-arn naming
// convention. Anything else stays unresolved and is dropped — a wrong infra
// edge is worse than a missing one.
func ScanTerraformSubscriptions(repoPath string) []QueueSubscription {
	if repoPath == "" {
		return nil
	}
	type subscription struct {
		topicExpr, endpointExpr, source string
	}
	sqsNames := map[string]string{} // terraform resource name -> queue name
	snsNames := map[string]string{} // terraform resource name -> topic name
	var subs []subscription

	_ = filepath.WalkDir(repoPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if name == ".git" || name == ".terraform" || name == "node_modules" || name == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".tf") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(repoPath, path)
		for _, m := range tfResourceRe.FindAllStringSubmatch(string(b), -1) {
			resType, resName, body := m[1], m[2], m[3]
			switch resType {
			case "aws_sqs_queue":
				if nm := tfNameAttrRe.FindStringSubmatch(body); nm != nil {
					sqsNames[resName] = nm[1]
				}
			case "aws_sns_topic":
				if nm := tfNameAttrRe.FindStringSubmatch(body); nm != nil {
					snsNames[resName] = nm[1]
				}
			case "aws_sns_topic_subscription":
				if proto := tfProtocolRe.FindStringSubmatch(body); proto == nil || proto[1] != "sqs" {
					continue
				}
				topic := tfTopicArnRe.FindStringSubmatch(body)
				endpoint := tfEndpointRe.FindStringSubmatch(body)
				if topic == nil || endpoint == nil {
					continue
				}
				subs = append(subs, subscription{
					topicExpr:    strings.TrimSpace(topic[1]),
					endpointExpr: strings.TrimSpace(endpoint[1]),
					source:       filepath.ToSlash(rel),
				})
			}
		}
		return nil
	})

	var out []QueueSubscription
	for _, s := range subs {
		topic := resolveTerraformTopic(s.topicExpr, snsNames)
		queue := resolveTerraformQueue(s.endpointExpr, sqsNames)
		if topic == "" || queue == "" {
			continue
		}
		out = append(out, QueueSubscription{Topic: topic, Queue: queue, Source: s.source})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Topic != out[j].Topic {
			return out[i].Topic < out[j].Topic
		}
		return out[i].Queue < out[j].Queue
	})
	return out
}

func resolveTerraformTopic(expr string, snsNames map[string]string) string {
	expr = strings.Trim(expr, `"`)
	if m := regexp.MustCompile(`\baws_sns_topic\.([A-Za-z0-9_-]+)\.arn\b`).FindStringSubmatch(expr); m != nil {
		return snsNames[m[1]]
	}
	if m := tfTopicVarRe.FindStringSubmatch(expr); m != nil {
		return m[1]
	}
	if strings.HasPrefix(expr, "arn:") {
		if i := strings.LastIndex(expr, ":"); i >= 0 && i+1 < len(expr) {
			return expr[i+1:]
		}
	}
	return ""
}

func resolveTerraformQueue(expr string, sqsNames map[string]string) string {
	expr = strings.Trim(expr, `"`)
	if m := regexp.MustCompile(`\baws_sqs_queue\.([A-Za-z0-9_-]+)\.arn\b`).FindStringSubmatch(expr); m != nil {
		return sqsNames[m[1]]
	}
	if strings.HasPrefix(expr, "arn:") {
		if i := strings.LastIndex(expr, ":"); i >= 0 && i+1 < len(expr) {
			return expr[i+1:]
		}
	}
	return ""
}
