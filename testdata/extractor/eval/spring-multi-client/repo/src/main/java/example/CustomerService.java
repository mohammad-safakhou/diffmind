package example;

public class CustomerService {

    private final CustomerRepository repo;

    public CustomerService(CustomerRepository repo) {
        this.repo = repo;
    }

    public Customer find(String id) {
        return repo.findById(id);
    }

    public void save(Customer customer) {
        repo.save(customer);
    }
}
