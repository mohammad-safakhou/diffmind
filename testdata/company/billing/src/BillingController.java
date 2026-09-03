package example;

import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RestController;

@RestController
public class BillingController {
    @GetMapping("/invoices")
    public String invoices() {
        return "[]";
    }
}
