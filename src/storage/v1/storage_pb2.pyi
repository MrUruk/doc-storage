from google.protobuf.internal import containers as _containers
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Iterable as _Iterable, Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class SaveFileRequest(_message.Message):
    __slots__ = ("file_name", "part")
    FILE_NAME_FIELD_NUMBER: _ClassVar[int]
    PART_FIELD_NUMBER: _ClassVar[int]
    file_name: str
    part: bytes
    def __init__(self, file_name: _Optional[str] = ..., part: _Optional[bytes] = ...) -> None: ...

class SaveFileResponse(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class SearchRequest(_message.Message):
    __slots__ = ("query", "limit")
    QUERY_FIELD_NUMBER: _ClassVar[int]
    LIMIT_FIELD_NUMBER: _ClassVar[int]
    query: str
    limit: int
    def __init__(self, query: _Optional[str] = ..., limit: _Optional[int] = ...) -> None: ...

class SearchResult(_message.Message):
    __slots__ = ("document_name", "html_content", "header", "score")
    DOCUMENT_NAME_FIELD_NUMBER: _ClassVar[int]
    HTML_CONTENT_FIELD_NUMBER: _ClassVar[int]
    HEADER_FIELD_NUMBER: _ClassVar[int]
    SCORE_FIELD_NUMBER: _ClassVar[int]
    document_name: str
    html_content: str
    header: str
    score: float
    def __init__(self, document_name: _Optional[str] = ..., html_content: _Optional[str] = ..., header: _Optional[str] = ..., score: _Optional[float] = ...) -> None: ...

class SearchResponse(_message.Message):
    __slots__ = ("results",)
    RESULTS_FIELD_NUMBER: _ClassVar[int]
    results: _containers.RepeatedCompositeFieldContainer[SearchResult]
    def __init__(self, results: _Optional[_Iterable[_Union[SearchResult, _Mapping]]] = ...) -> None: ...
