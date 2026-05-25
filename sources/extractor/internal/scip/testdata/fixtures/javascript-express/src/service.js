// CampaignService is the intermediate hop the walker must follow.
//
// See handlers.js for the JSDoc rationale; the same `@type` pattern
// links `this.repo` to CampaignRepository so the walker can step
// from getById → CampaignRepository.findById.

const { CampaignRepository } = require("./repository");

class CampaignService {
  /** @param {CampaignRepository} repo */
  constructor(repo) {
    /** @type {CampaignRepository} */
    this.repo = repo;
  }

  getById(id) {
    if (!id) {
      throw new Error("id required");
    }
    const value = this.repo.findById(id);
    if (!value) {
      throw new Error("not found");
    }
    return value;
  }

  create(id, value) {
    if (!id || !value) {
      throw new Error("invalid");
    }
    this.repo.save(id, value);
  }
}

module.exports = { CampaignService };
