# VS Code IntelliSense

Terragraph Blueprint 확장은 `blueprint.hcl`과 `group.hcl`을 편집할 때
언어 서버 기반 편집 기능을 제공합니다. VS Code Marketplace에서
**Terragraph Blueprint** 확장을 설치하면 바로 사용할 수 있습니다.

확장에는 호환되는 언어 서버가 함께 포함되므로, IntelliSense만 사용하려고
`terragraph` CLI를 따로 설치할 필요는 없습니다.

## 자동 완성

`Ctrl+Space` 또는 macOS의 `Control+Space`로 완성 목록을 열 수 있습니다.

- 최상위 Blueprint block: `node`, `edge`, `runtime`, `group`, `use`,
  `vendor`, `tfvars`
- 각 block에서 사용할 수 있는 속성: 예를 들어 node의 `source`, `vars`,
  `env`, `runtime`, `backend_config`
- Terraform/OpenTofu 모듈의 input 변수와 output 값
- 선언된 runtime 이름

`vars = {}` 내부에서는 해당 node의 모듈이 선언한 input 변수만 제안됩니다.
다른 node의 결과를 넘길 때는 `vars`가 아니라 `edge`를 사용합니다.

```hcl
edge {
  from = node.vpc.output.vpc_id
  to   = node.eks.input.vpc_id
}
```

input 완성 항목에는 타입, required 여부, sensitive 여부, description이
표시됩니다. output은 이름과 description, sensitive 여부만 표시합니다.

## 정의로 이동

`node.vpc` 또는 `runtime.tofu`에서 `Cmd+Click`(macOS), `Ctrl+Click`
(Windows/Linux), 또는 `F12`를 누르면 해당 선언으로 이동합니다. 같은
Blueprint 디렉터리의 다른 `.hcl` 파일에 선언한 node와 runtime도 찾습니다.

## 오류 표시

다음과 같이 정적으로 확인할 수 있는 오류는 입력 중에도 빨간 밑줄로
표시됩니다.

- 존재하지 않는 node 이름
- 존재하지 않는 module input 또는 output 이름
- `from`에서 input을 참조하거나 `to`에서 output을 참조한 경우
- `vars`에 모듈에 없는 input 변수를 설정한 경우

오류 위에 마우스를 올리면 가능한 input 또는 output 이름을 확인할 수
있습니다.

## 언어 서버 경로를 직접 지정하기

보통은 필요하지 않지만, 개발 중인 바이너리나 특정 버전의 CLI를 사용하려면
VS Code 설정에 다음 값을 넣습니다.

```json
{
  "terragraph.languageServer.path": "/absolute/path/to/terragraph"
}
```

경로를 비우면 확장에 포함된 언어 서버를 다시 사용합니다.

## 자동 완성이 보이지 않을 때

1. 파일 이름이 `blueprint.hcl` 또는 `group.hcl`인지 확인합니다.
2. `Developer: Reload Window` 명령으로 VS Code 창을 다시 로드합니다.
3. **View: Output**에서 `Terragraph Blueprint` 채널을 선택해 언어 서버
   시작 오류를 확인합니다.
4. 개발 중이라면 저장소 루트에서 `make build`를 실행한 뒤 Extension
   Development Host를 재시작합니다.
