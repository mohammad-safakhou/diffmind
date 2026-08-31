"""HTTP handlers. The integration test anchors on `get_campaign` at
line 8 (1-based) below."""

from .service import CampaignService


def get_campaign(svc: CampaignService, id: str) -> str:
    # line 8 anchor: this is the symbol resolver target.
    return svc.get_by_id(id)


def create_campaign(svc: CampaignService, id: str, value: str) -> None:
    svc.create(id, value)
