package example;

import java.util.List;

public record CheckoutRequest(
    String customerId,
    String deliveryPostalCode,
    String deliveryWindow,
    String market,
    List<String> items
) {}
