from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="CreateGoogleSessionBody")


@_attrs_define
class CreateGoogleSessionBody:
    """
    Attributes:
        credential (str): The ID token that Google Identity Services gave the page.
        schema (str | Unset): A URL to the JSON Schema for this object.
        display_name (str | Unset): The display name of a new account: 3 to 24 characters of letters, digits, spaces, -
            and _, unique among accounts without regard to case. Required on an account's first sign-in, ignored after it.
    """

    credential: str
    schema: str | Unset = UNSET
    display_name: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        credential = self.credential

        schema = self.schema

        display_name = self.display_name

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "credential": credential,
            }
        )
        if schema is not UNSET:
            field_dict["$schema"] = schema
        if display_name is not UNSET:
            field_dict["display_name"] = display_name

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        credential = d.pop("credential")

        schema = d.pop("$schema", UNSET)

        display_name = d.pop("display_name", UNSET)

        create_google_session_body = cls(
            credential=credential,
            schema=schema,
            display_name=display_name,
        )

        return create_google_session_body
