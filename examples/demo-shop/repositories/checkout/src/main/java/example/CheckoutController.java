package example;

import org.springframework.web.bind.annotation.PostMapping;
import org.springframework.web.bind.annotation.RequestBody;
import org.springframework.web.bind.annotation.RestController;

@RestController
public class CheckoutController {
    @PostMapping("/v1/checkout")
    public String checkout(@RequestBody CheckoutRequest request) {
        return "accepted";
    }
}
