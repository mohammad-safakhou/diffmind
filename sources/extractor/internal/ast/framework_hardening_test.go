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
