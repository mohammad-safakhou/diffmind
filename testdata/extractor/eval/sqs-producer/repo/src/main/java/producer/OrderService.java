package producer;

import org.springframework.stereotype.Service;

@Service
public class OrderService {
    private final OrderEventPublisher publisher;

    public OrderService(OrderEventPublisher publisher) {
        this.publisher = publisher;
    }

    public void create(String order) {
        publisher.publish(order);
    }
}
