package consumer;

import io.awspring.cloud.sqs.annotation.SqsListener;
import org.springframework.stereotype.Component;

@Component
public class OrderEventsListener {
    private final OrderProjectionRepository repository;

    public OrderEventsListener(OrderProjectionRepository repository) {
        this.repository = repository;
    }

    @SqsListener("${services.aws.sqs.order-events.url}")
    public void onOrderEvent(String payload) {
        repository.save(new OrderProjection(payload));
    }
}
