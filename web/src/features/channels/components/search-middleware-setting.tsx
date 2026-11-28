/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { useId } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import {
  Field,
  FieldContent,
  FieldDescription,
  FieldGroup,
  FieldLabel,
} from '@/components/ui/field'
import { Switch } from '@/components/ui/switch'

type SearchMiddlewareSettingProps = {
  enabled: boolean
  disabled: boolean
  onEnabledChange: (enabled: boolean) => void
  onEditMapping?: () => void
}

export function SearchMiddlewareSetting(props: SearchMiddlewareSettingProps) {
  const { t } = useTranslation()
  const id = useId()
  const descriptionId = `${id}-description`

  return (
    <FieldGroup className='gap-1'>
      <Field orientation='horizontal' data-disabled={props.disabled}>
        <Switch
          id={id}
          nativeButton
          render={<button type='button' />}
          checked={props.enabled}
          disabled={props.disabled}
          onCheckedChange={props.onEnabledChange}
          aria-describedby={props.onEditMapping ? descriptionId : undefined}
        />
        <FieldContent>
          <FieldLabel htmlFor={id}>{t('Enable search middleware')}</FieldLabel>
          {props.onEditMapping && (
            <FieldDescription id={descriptionId}>
              {t('Codex search requires an upstream model.')}
            </FieldDescription>
          )}
        </FieldContent>
      </Field>
      {props.onEditMapping && (
        <Button
          type='button'
          variant='link'
          size='sm'
          className='w-fit'
          disabled={props.disabled}
          onClick={props.onEditMapping}
        >
          {t('Map search model')}
        </Button>
      )}
    </FieldGroup>
  )
}
