package example;

import org.springframework.data.jpa.repository.JpaRepository;
import org.springframework.stereotype.Repository;

// Two distinct repository clients (OrderRepository, CustomerRepository) share
// one datasource. The op→client linker must bind repo.findById/save through the
// correct repository type so each op attributes to its own physical table; the
// AST client floor detects both as db clients on spring.datasource.url.
@Repository
public interface OrderRepository extends JpaRepository<Order, String> {
    Order findById(String id);

    Order save(Order order);
}
