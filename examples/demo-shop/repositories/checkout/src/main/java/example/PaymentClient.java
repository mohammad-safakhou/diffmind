package example;

import org.springframework.cloud.openfeign.FeignClient;
import org.springframework.web.bind.annotation.PostMapping;
import org.springframework.web.bind.annotation.RequestBody;

@FeignClient(name = "payment", url = "http://payment")
public interface PaymentClient {
    @PostMapping("/v1/payments")
    String createPayment(@RequestBody String request);
}
