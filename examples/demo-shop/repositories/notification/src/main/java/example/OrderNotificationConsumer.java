package example;

import org.springframework.kafka.annotation.KafkaListener;

public class OrderNotificationConsumer {
    @KafkaListener(topics = "orders.created")
    public void notifyCustomer(String order) {
        // Synthetic fixture: no message is sent outside the demo.
    }
}
