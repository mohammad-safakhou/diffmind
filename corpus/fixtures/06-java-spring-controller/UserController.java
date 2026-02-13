class UserController {
  @GetMapping("/users")
  String users() { return "ok"; }
  @Value("${DB_URL}")
  String db;
}
