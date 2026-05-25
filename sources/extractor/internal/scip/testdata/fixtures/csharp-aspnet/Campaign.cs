namespace ScipFixture;

// Trivial value object returned by repository queries.
public sealed class Campaign
{
    public string Id { get; }
    public Campaign(string id) => Id = id;
}
