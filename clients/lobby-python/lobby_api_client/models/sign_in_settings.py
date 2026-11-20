from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="SignInSettings")


@_attrs_define
class SignInSettings:
    """
    Attributes:
        google_client_id (None | str): The Google OAuth client ID that a Sign in with Google button uses, or null while
            sign-in is off.
        schema (str | Unset): A URL to the JSON Schema for this object.
    """

    google_client_id: None | str
    schema: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        google_client_id: None | str
        google_client_id = self.google_client_id

        schema = self.schema

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "google_client_id": google_client_id,
            }
        )
        if schema is not UNSET:
            field_dict["$schema"] = schema

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)

        def _parse_google_client_id(data: object) -> None | str:
            if data is None:
                return data
            return cast(None | str, data)

        google_client_id = _parse_google_client_id(d.pop("google_client_id"))

        schema = d.pop("$schema", UNSET)

        sign_in_settings = cls(
            google_client_id=google_client_id,
            schema=schema,
        )

        return sign_in_settings
