package example;

import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.PostMapping;
import org.springframework.web.bind.annotation.PathVariable;
import org.springframework.web.bind.annotation.RequestBody;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RestController;

// A minimal but realistic Spring CRUD controller used as a deterministic-floor
// eval fixture: two annotated routes whose handlers fan into a service →
// repository, so the floor must produce http_route exposures, db_operation
// dependencies, and the connections between them.
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
