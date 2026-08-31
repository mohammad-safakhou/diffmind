package example;

import javax.persistence.Entity;
import javax.persistence.Table;

// @Table makes the physical table name differ from the class name, so the
// deterministic table harvest must read "orders" from the annotation rather
// than lowercasing the entity class.
@Entity
@Table(name = "orders")
public class Order {
    private String id;

    public String getId() {
        return id;
    }

    public void setId(String id) {
        this.id = id;
    }
}
