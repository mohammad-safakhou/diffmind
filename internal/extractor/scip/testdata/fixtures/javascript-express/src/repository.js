// CampaignRepository is the data-access layer. The integration test
// asserts the walker reaches `findById` through the service.

class CampaignRepository {
  constructor() {
    this.store = new Map();
  }

  findById(id) {
    return this.store.get(id);
  }

  save(id, value) {
    this.store.set(id, value);
  }
}

module.exports = { CampaignRepository };
