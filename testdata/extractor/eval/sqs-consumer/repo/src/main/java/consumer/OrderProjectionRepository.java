package consumer;

import org.springframework.data.repository.CrudRepository;
import org.springframework.stereotype.Repository;

// save is declared explicitly (not just inherited) so the cross-file call
// resolver can bind repository.save to this type and the db deriver can
// attribute it to the `order_projection` table (same pattern as spring-crud).
@Repository
public interface OrderProjectionRepository extends CrudRepository<OrderProjection, Long> {
    OrderProjection save(OrderProjection entity);
}
