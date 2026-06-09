package ast_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/ast"
	_ "github.com/mohammad-safakhou/diffmind/internal/ast/framework"
)

func TestSpringDetectorRequiresControllerAndSeparatesFeign(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "UserController.java", `package com.example;

import org.springframework.web.bind.annotation.*;

@RestController
@RequestMapping("/users")
public class UserController {
    @GetMapping
    public String list() { return "ok"; }

    @PostMapping({"/one", "/two"})
    public String create() { return "ok"; }
}
`)
	writeFile(t, dir, "RemoteClient.java", `package com.example;

import org.springframework.cloud.openfeign.FeignClient;
import org.springframework.web.bind.annotation.GetMapping;

@FeignClient(name = "remote")
public interface RemoteClient {
    @GetMapping("/remote/{id}")
    String getRemote(String id);
}
`)
	writeFile(t, dir, "PlainService.java", `package com.example;

import org.springframework.web.bind.annotation.GetMapping;

public class PlainService {
    @GetMapping("/not-inbound")
    public String notARoute() { return "no"; }
}
`)

	idx := buildIndex(t, dir)
	assertBinding(t, idx.Frameworks, "spring", "http_handler", "GET /users", "UserController.list")
	assertBinding(t, idx.Frameworks, "spring", "http_handler", "POST /users/one", "UserController.create")
	assertBinding(t, idx.Frameworks, "spring", "http_handler", "POST /users/two", "UserController.create")
	assertBinding(t, idx.Frameworks, "spring", "http_client", "GET /remote/{id}", "RemoteClient.getRemote")
	assertNoBinding(t, idx.Frameworks, "spring", "http_handler", "GET /remote/{id}")
	assertNoBinding(t, idx.Frameworks, "spring", "http_handler", "GET /not-inbound")
	assertRejected(t, idx.RejectedFrameworks, "spring_mapping_without_controller_context")
}

func TestJavaCriteriaGetDoesNotBecomeExpressRoute(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "CriteriaRepo.java", `package com.example;

public class CriteriaRepo {
    public void query(Root root) {
        root.get("FIELD_ID");
        attributes.get("ATTRIBUTE_TABLE_NAME");
    }
}

interface Root {
    Object get(String name);
}
`)
	idx := buildIndex(t, dir)
	for _, b := range idx.Frameworks {
		if b.Framework == "express" {
			t.Fatalf("java .get call became express binding: %+v", b)
		}
	}
}

func TestNestDetectorRequiresControllerContext(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "users.controller.ts", `import { Controller, Get } from '@nestjs/common';

@Controller('users')
export class UsersController {
  @Get(':id')
  getUser() { return 'ok'; }
}

export class PlainClass {
  @Get('wrong')
  notRoute() { return 'no'; }
}
`)
	idx := buildIndex(t, dir)
	assertBinding(t, idx.Frameworks, "nestjs", "http_handler", "GET /users/:id", "UsersController.getUser")
	assertNoBinding(t, idx.Frameworks, "nestjs", "http_handler", "GET /wrong")
	assertRejected(t, idx.RejectedFrameworks, "nestjs_http_decorator_without_controller_context")
}

func TestAspNetDetectorComposesControllerPrefix(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "UsersController.cs", `using Microsoft.AspNetCore.Mvc;

[ApiController]
[Route("api/users")]
public class UsersController : ControllerBase
{
    [HttpGet("{id}")]
    public string GetUser(string id) { return id; }
}
`)
	idx := buildIndex(t, dir)
	assertBinding(t, idx.Frameworks, "aspnet", "http_handler", "GET /api/users/{id}", "UsersController.GetUser")
	assertNoBinding(t, idx.Frameworks, "aspnet", "http_handler", "ANY /api/users")
}

func TestGoRouteDetectorRequiresLiteralPathAndHandler(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "main.go", `package main

func main() {
    r := Router{}
    r.GET("/users", listUsers)
    r.GET(dynamicPath(), listUsers)
    root.get("FIELD_ID")
}

func listUsers() {}
func dynamicPath() string { return "/dynamic" }

type Router struct{}
func (Router) GET(path string, handler func()) {}

type Root struct{}
func (Root) get(name string) {}
`)
	idx := buildIndex(t, dir)
	assertBinding(t, idx.Frameworks, "gin", "http_handler", "GET /users", "main")
	assertNoBinding(t, idx.Frameworks, "gin", "http_handler", "GET /dynamic")
	assertNoBinding(t, idx.Frameworks, "express", "http_handler", "GET /FIELD_ID")
}

func TestExpressDetectorRequiresKnownReceiverLiteralPathAndHandler(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "routes.js", `function handler(req, res) {}

app.get('/users', handler)
client.get('/not-route', handler)
router.post(dynamicPath(), handler)
`)
	idx := buildIndex(t, dir)
	assertBinding(t, idx.Frameworks, "express", "http_handler", "GET /users", "")
	assertNoBinding(t, idx.Frameworks, "express", "http_handler", "GET /not-route")
	assertNoBinding(t, idx.Frameworks, "express", "http_handler", "POST /dynamic")
}

// E1: route paths must come only from the positional arg or value=/path=,
// never from produces/consumes/headers/params — otherwise those string literals
// are fabricated into phantom routes at confidence 1.0.
func TestSpringRouteIgnoresProducesConsumesAttributes(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "OrderController.java", `package com.example;

import org.springframework.web.bind.annotation.*;

@RestController
@RequestMapping("/api")
public class OrderController {
    @PostMapping(value = "/orders", produces = "application/json")
    public String create() { return "ok"; }

    @GetMapping(produces = "application/json")
    public String list() { return "ok"; }

    @PutMapping(path = "/orders/{id}", consumes = "application/json")
    public String update() { return "ok"; }
}
`)
	idx := buildIndex(t, dir)
	// Real routes.
	assertBinding(t, idx.Frameworks, "spring", "http_handler", "POST /api/orders", "OrderController.create")
	assertBinding(t, idx.Frameworks, "spring", "http_handler", "GET /api", "OrderController.list")
	assertBinding(t, idx.Frameworks, "spring", "http_handler", "PUT /api/orders/{id}", "OrderController.update")
	// No phantom routes fabricated from the media-type literal.
	assertNoBinding(t, idx.Frameworks, "spring", "http_handler", "POST /api/application/json")
	assertNoBinding(t, idx.Frameworks, "spring", "http_handler", "GET /api/application/json")
	assertNoBinding(t, idx.Frameworks, "spring", "http_handler", "PUT /api/application/json")
}

// E1: a class-level @RequestMapping with a produces attribute must not turn the
// media type into a second class prefix.
func TestSpringClassMappingIgnoresProduces(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "V2Controller.java", `package com.example;

import org.springframework.web.bind.annotation.*;

@RestController
@RequestMapping(value = "/v2", produces = "application/json")
public class V2Controller {
    @GetMapping("/ping")
    public String ping() { return "ok"; }
}
`)
	idx := buildIndex(t, dir)
	assertBinding(t, idx.Frameworks, "spring", "http_handler", "GET /v2/ping", "V2Controller.ping")
	assertNoBinding(t, idx.Frameworks, "spring", "http_handler", "GET /application/json/ping")
}

// E2: array-valued listener destinations must yield one consumer per
// destination, not a single mangled `{"a` trigger with the rest dropped.
func TestSpringListenerArrayDestinations(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "Listeners.java", `package com.example;

import org.springframework.kafka.annotation.KafkaListener;
import io.awspring.cloud.sqs.annotation.SqsListener;

public class Listeners {
    @KafkaListener(topics = {"orders", "shipments"}, groupId = "g1")
    public void onKafka(String m) {}

    @SqsListener(value = "single-queue")
    public void onSqs(String m) {}
}
`)
	idx := buildIndex(t, dir)
	assertBinding(t, idx.Frameworks, "spring", "queue_consumer", "kafka: orders", "Listeners.onKafka")
	assertBinding(t, idx.Frameworks, "spring", "queue_consumer", "kafka: shipments", "Listeners.onKafka")
	assertBinding(t, idx.Frameworks, "spring", "queue_consumer", "sqs: single-queue", "Listeners.onSqs")
	// The mangled single-topic form must not survive.
	assertNoBinding(t, idx.Frameworks, "spring", "queue_consumer", `kafka: {"orders`)
	// A sibling string attribute (groupId) must NOT be picked as the topic.
	assertNoBinding(t, idx.Frameworks, "spring", "queue_consumer", "kafka: g1")
}

// E2 regression: real Spring Cloud AWS @SqsListener uses queueNames= (not value=);
// the destination must still be extracted (and not dropped to an empty name).
func TestSpringSqsListenerQueueNamesAttribute(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "AtsListener.java", `package com.example;

import io.awspring.cloud.sqs.annotation.SqsListener;

public class AtsListener {
    @SqsListener(queueNames = "${services.aws.sqs.ats-events-sqs.url}", factory = "f")
    public void onMessage(String m) {}
}
`)
	idx := buildIndex(t, dir)
	assertBinding(t, idx.Frameworks, "spring", "queue_consumer",
		"sqs: ${services.aws.sqs.ats-events-sqs.url}", "AtsListener.onMessage")
	// Must not collapse to an empty destination (the pre-fix regression).
	assertNoBinding(t, idx.Frameworks, "spring", "queue_consumer", "sqs: ")
}

func buildIndex(t *testing.T, dir string) *ast.ProjectIndex {
	t.Helper()
	idx, err := ast.Build(context.Background(), dir, "", 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	return idx
}

func assertBinding(t *testing.T, bindings []ast.FrameworkBinding, framework, kind, trigger, symbolSuffix string) {
	t.Helper()
	for _, b := range bindings {
		if b.Framework == framework && b.Kind == kind && b.Trigger == trigger && strings.HasSuffix(b.Symbol, symbolSuffix) {
			return
		}
	}
	t.Fatalf("binding not found: framework=%s kind=%s trigger=%s symbol=%s; got %+v", framework, kind, trigger, symbolSuffix, bindings)
}

func assertNoBinding(t *testing.T, bindings []ast.FrameworkBinding, framework, kind, trigger string) {
	t.Helper()
	for _, b := range bindings {
		if b.Framework == framework && b.Kind == kind && b.Trigger == trigger {
			t.Fatalf("unexpected binding: %+v", b)
		}
	}
}

func assertRejected(t *testing.T, bindings []ast.FrameworkBinding, reason string) {
	t.Helper()
	for _, b := range bindings {
		if b.RejectionReason == reason {
			return
		}
	}
	t.Fatalf("rejected binding reason %q not found; got %+v", reason, bindings)
}
