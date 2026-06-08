package example;

import org.springframework.data.jpa.repository.JpaRepository;
import org.springframework.stereotype.Repository;

// findById and save are declared explicitly (not just inherited) so the
// cross-file call resolver can bind repo.findById / repo.save to this type and
// the db deriver can attribute them to the `order` table.
@Repository
public interface OrderRepository extends JpaRepository<Order, String> {
    Order findById(String id);

    Order save(Order order);
}
