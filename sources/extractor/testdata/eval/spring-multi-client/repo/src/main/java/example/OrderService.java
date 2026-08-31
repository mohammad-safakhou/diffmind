package example;

public class OrderService {

    private final OrderRepository repo;

    public OrderService(OrderRepository repo) {
        this.repo = repo;
    }

    public Order find(String id) {
        return repo.findById(id);
    }

    public void save(Order order) {
        repo.save(order);
    }
}
