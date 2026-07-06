package detectors

import "testing"

func TestIDsForFrameworkBinding(t *testing.T) {
	tests := []struct {
		name      string
		framework string
		kind      string
		trigger   string
		reason    string
		want      string
	}{
		{name: "spring route", framework: "spring", kind: "http_handler", want: "java.http.spring"},
		{name: "spring feign client", framework: "spring", kind: "http_client", want: "java.httpclient.feign"},
		{name: "retrofit client", framework: "retrofit", kind: "http_client", want: "java.httpclient.retrofit"},
		{name: "spring kafka listener", framework: "spring", kind: "queue_consumer", trigger: "kafka:campaigns", want: "java.queue.kafka"},
		{name: "spring sqs listener", framework: "spring", kind: "queue_consumer", trigger: "sqs:jobs", want: "java.queue.sqs"},
		{name: "spring sns publisher", framework: "spring", kind: "queue_publisher", trigger: "sns:campaigns", want: "java.queue.sqs"},
		{name: "spring rabbit publisher", framework: "spring", kind: "queue_publisher", trigger: "rabbitmq:orders.created", want: "java.queue.rabbitmq"},
		{name: "spring jms publisher", framework: "spring", kind: "queue_publisher", trigger: "jms:orders", want: "java.queue.jms"},
		{name: "flask route", framework: "flask", kind: "http_handler", want: "python.http.flask"},
		{name: "go fiber route", framework: "fiber", kind: "http_handler", want: "golang.http.fiber"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IDsForFrameworkBinding(tt.framework, tt.kind, tt.trigger, tt.reason)
			for _, id := range got {
				if id == tt.want {
					return
				}
			}
			t.Fatalf("IDsForFrameworkBinding() = %v, want to include %q", got, tt.want)
		})
	}
}

func TestAllowFrameworkBinding(t *testing.T) {
	if !AllowFrameworkBinding([]string{"java.http.spring"}, nil, nil) {
		t.Fatal("empty config should allow known detector")
	}
	if !AllowFrameworkBinding(nil, nil, nil) {
		t.Fatal("empty config should allow unknown detector for compatibility")
	}
	if !AllowFrameworkBinding([]string{"java.http.spring"}, []string{"python.http.flask"}, nil) {
		t.Fatal("enabled list is deprecated and should not filter detectors")
	}
	if AllowFrameworkBinding([]string{"java.http.spring"}, []string{"java.http.spring"}, []string{"java.http.spring"}) {
		t.Fatal("disabled detector should suppress detector output")
	}
}
