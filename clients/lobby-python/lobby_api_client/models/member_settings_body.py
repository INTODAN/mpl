from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.member_settings_body_member_settings import MemberSettingsBodyMemberSettings


T = TypeVar("T", bound="MemberSettingsBody")


@_attrs_define
class MemberSettingsBody:
    """
    Attributes:
        member_settings (MemberSettingsBodyMemberSettings): Your own settings for the lobby's game, as a JSON object of
            at most 4 KiB that only the game reads, such as what plays for you. The game checks them; settings it doesn't
            accept fail with invalid_member_settings, and a game that takes none refuses every value.
        schema (str | Unset): A URL to the JSON Schema for this object.
    """

    member_settings: MemberSettingsBodyMemberSettings
    schema: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        member_settings = self.member_settings.to_dict()

        schema = self.schema

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "member_settings": member_settings,
            }
        )
        if schema is not UNSET:
            field_dict["$schema"] = schema

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.member_settings_body_member_settings import MemberSettingsBodyMemberSettings

        d = dict(src_dict)
        member_settings = MemberSettingsBodyMemberSettings.from_dict(d.pop("member_settings"))

        schema = d.pop("$schema", UNSET)

        member_settings_body = cls(
            member_settings=member_settings,
            schema=schema,
        )

        return member_settings_body
