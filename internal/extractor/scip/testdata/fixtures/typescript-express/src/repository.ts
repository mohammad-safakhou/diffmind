// CampaignRepository is the data-access layer. The integration test
// asserts the walker reaches `findById` through the service.
export class CampaignRepository {
  private store = new Map<string, string>();

  findById(id: string): string | undefined {
    return this.store.get(id);
  }

  save(id: string, value: string): void {
    this.store.set(id, value);
  }
}
