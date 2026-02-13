class App {
  public static void main(String[] args) {
    restTemplate.getForObject("https://orders.example.com", String.class);
  }
}
