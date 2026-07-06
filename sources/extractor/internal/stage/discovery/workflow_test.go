package discovery

import "testing"

func TestDeterministicWorkflowOrchestrationDetectsCamundaExternalTaskWorker(t *testing.T) {
	idx := buildAgentsIndex(t, map[string]string{
		"src/main/resources/application.yml": `
external-task:
  url: https://cdp-${UUID}-cdp-stories-camunda.aws-sdlc-example.com/rest
`,
		"src/main/java/com/acme/InitialPayloadBuilderTaskClient.java": `
package com.acme;

class InitialPayloadBuilderTaskClient extends AbstractExternalTaskClient {
  private static final String TOPIC = "stories::import::build_initial_payload";
  private static final String CHANGE_MEDIA_TTL_URL_VAR_NAME = "changeMediaTtlUrl";

  InitialPayloadBuilderTaskClient(ExternalTaskOptions options) {
    super(options, TOPIC);
  }

  ExternalTaskTaskHandler getExternalTaskHandler() {
    return externalTask -> ExternalTaskResult.builder()
      .addVariable(CHANGE_MEDIA_TTL_URL_VAR_NAME, changeMediaTtlUrl)
      .build();
  }
}
`,
	})

	got := DeterministicWorkflowOrchestration(idx)
	if len(got) != 1 {
		t.Fatalf("expected one workflow orchestration candidate, got %d: %+v", len(got), got)
	}
	if got[0].Type != "workflow_orchestration" {
		t.Fatalf("unexpected type: %+v", got[0])
	}
	if got[0].Details["orchestrator"] != "camunda" {
		t.Fatalf("expected camunda orchestrator, got %+v", got[0].Details)
	}
	if got[0].Details["target_service"] != "cdp-stories-camunda" {
		t.Fatalf("expected camunda target service from config URL, got %+v", got[0].Details)
	}
	if got[0].Details["topic"] != "stories::import::build_initial_payload" {
		t.Fatalf("expected external task topic, got %+v", got[0].Details)
	}
	if got[0].Details["invocation_mode"] != "external_task_worker" {
		t.Fatalf("expected external task invocation mode, got %+v", got[0].Details)
	}
	vars, ok := got[0].Details["callback_variables"].([]string)
	if !ok || len(vars) != 1 || vars[0] != "changeMediaTtlUrl" {
		t.Fatalf("expected callback variable evidence, got %+v", got[0].Details["callback_variables"])
	}
}
