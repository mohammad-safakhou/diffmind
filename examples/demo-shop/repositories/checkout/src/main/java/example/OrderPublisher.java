package example;

import org.springframework.kafka.core.KafkaTemplate;

public class OrderPublisher {
    private KafkaTemplate<String, String> template;

    public void publish(String order) {
        template.send("orders.created", order);
    }
}
