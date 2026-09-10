package example;

import java.util.List;

public record CheckoutRequest(String customerId, String postalCode, List<String> items) {}
