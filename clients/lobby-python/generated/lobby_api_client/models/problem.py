from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.problem_code import ProblemCode

T = TypeVar("T", bound="Problem")


@_attrs_define
class Problem:
    """
    Attributes:
        code (ProblemCode):
        detail (str):
        status (int):
        title (str):
    """

    code: ProblemCode
    detail: str
    status: int
    title: str

    def to_dict(self) -> dict[str, Any]:
        code = self.code.value

        detail = self.detail

        status = self.status

        title = self.title

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "code": code,
                "detail": detail,
                "status": status,
                "title": title,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        code = ProblemCode(d.pop("code"))

        detail = d.pop("detail")

        status = d.pop("status")

        title = d.pop("title")

        problem = cls(
            code=code,
            detail=detail,
            status=status,
            title=title,
        )

        return problem
