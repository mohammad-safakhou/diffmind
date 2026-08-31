package example;

/**
 * Campaign is the value object returned by repository queries. It is
 * intentionally trivial; the integration test cares about the call
 * graph, not the data model.
 */
public class Campaign {
    private final String id;

    public Campaign(String id) {
        this.id = id;
    }

    public String getId() {
        return id;
    }
}
