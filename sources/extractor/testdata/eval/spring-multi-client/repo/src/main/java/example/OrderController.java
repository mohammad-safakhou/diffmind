package example;

import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.PostMapping;
import org.springframework.web.bind.annotation.PathVariable;
import org.springframework.web.bind.annotation.RequestBody;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RestController;

// Routes fan into the order service → order repository. The floor must attribute
// these db ops to the orders table (not customers), proving op→client linking
// picks the right one of two same-kind clients.
@RestController
@RequestMapping("/orders")
public class OrderController {

    private final OrderService service;

    public OrderController(OrderService service) {
        this.service = service;
    }

    @GetMapping("/{id}")
    public Order get(@PathVariable String id) {
        return service.find(id);
    }

    @PostMapping
    public void create(@RequestBody Order order) {
        service.save(order);
    }
}
