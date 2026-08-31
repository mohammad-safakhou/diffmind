package producer;

import io.awspring.cloud.sqs.operations.SqsTemplate;
import org.springframework.stereotype.Component;

@Component
public class OrderEventPublisher {
    private final SqsTemplate sqsTemplate;

    public OrderEventPublisher(SqsTemplate sqsTemplate) {
        this.sqsTemplate = sqsTemplate;
    }

    public void publish(String payload) {
        sqsTemplate.send("${services.aws.sqs.order-events.url}", payload);
    }
}
