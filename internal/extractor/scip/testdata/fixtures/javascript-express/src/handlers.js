// handlers.js contains the HTTP entry points. The integration test
// targets `getCampaign` declared around line 10 (1-based) below.
//
// IMPORTANT for scip-typescript on JS: TypeScript's checker is the
// engine behind the indexer, and for plain JS it relies on JSDoc
// `@type` annotations to resolve cross-module references. Without
// them `this.svc` is `any` and `this.svc.getById(...)` resolves to
// the bare property name with no link to CampaignService. Real
// Node codebases use the same trick to get IntelliSense.

const { CampaignService } = require("./service");

class CampaignHandlers {
  /** @param {CampaignService} svc */
  constructor(svc) {
    /** @type {CampaignService} */
    this.svc = svc;
  }

  // Anchor: getCampaign is the entry symbol the resolver looks up.
  getCampaign(id) {
    return this.svc.getById(id);
  }

  createCampaign(id, value) {
    this.svc.create(id, value);
  }
}

module.exports = { CampaignHandlers };
