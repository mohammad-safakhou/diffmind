package consumer;

import jakarta.persistence.Entity;
import jakarta.persistence.Id;

@Entity
public class OrderProjection {
    @Id
    private Long id;
    private String payload;

    public OrderProjection() {
    }

    public OrderProjection(String payload) {
        this.payload = payload;
    }
}
